import com.google.gson.*;
import com.sun.net.httpserver.*;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.MessageDigest;
import java.sql.*;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import java.util.concurrent.locks.ReentrantLock;

/** Internal JDBC protocol v1. User authorization and durable receipts belong to Go. */
public final class DBHubJdbc {
    static final Gson JSON = new GsonBuilder().disableHtmlEscaping().create();
    static final Map<String, Artifact> ARTIFACTS = new LinkedHashMap<>();
    static final ConcurrentHashMap<String, Session> SESSIONS = new ConcurrentHashMap<>();
    static final Semaphore SESSION_SLOTS = new Semaphore(32);
    static final ScheduledExecutorService TIMER = Executors.newScheduledThreadPool(2, daemon("jdbc-timer"));
    static final ExecutorService ABORT = new ThreadPoolExecutor(4,4,0,TimeUnit.SECONDS,new ArrayBlockingQueue<>(64),daemon("jdbc-abort"),new ThreadPoolExecutor.AbortPolicy());
    static final ThreadPoolExecutor OPENERS = new ThreadPoolExecutor(0, 4, 30, TimeUnit.SECONDS, new SynchronousQueue<>(), daemon("jdbc-open"), new ThreadPoolExecutor.AbortPolicy());
    static final String TOKEN = System.getenv("DBHUB_JDBC_TOKEN");
    static final long LEASE = TimeUnit.MINUTES.toNanos(5);

    static ThreadFactory daemon(String name) { return r -> {Thread t=new Thread(r,name);t.setDaemon(true);return t;}; }
    static Map<String,Object> obj(Object... pairs) {Map<String,Object> m=new LinkedHashMap<>();for(int i=0;i<pairs.length;i+=2)m.put((String)pairs[i],pairs[i+1]);return m;}
    static String str(JsonObject o,String k) {JsonElement v=o.get(k);return v==null||v.isJsonNull()?"":v.getAsString();}
    static boolean bool(JsonObject o,String k) {return o.has(k)&&o.get(k).getAsBoolean();}
    static int number(JsonObject o,String k,int fallback) {return o.has(k)?o.get(k).getAsInt():fallback;}
    static final class Problem extends RuntimeException {final String kind,state;final int status;final boolean definite;Problem(int status,String kind,String message){this(status,kind,message,"",false);}Problem(int status,String kind,String message,String state,boolean definite){super(message);this.status=status;this.kind=kind;this.state=state;this.definite=definite;}}
    record Artifact(String id,String name,String version,String className,String sha256,Driver driver,URLClassLoader loader) {
        Map<String,Object> summary(){return obj("id",id,"name",name,"version",version,"class_name",className,"sha256",sha256,"ready",true);}
    }
    static final class Session {
        final Connection connection;
        final Artifact artifact;
        final String owner,database,dialect;
        final ReentrantLock lock=new ReentrantLock();
        final AtomicReference<Statement> statement=new AtomicReference<>();
        final Set<String> executed=new HashSet<>();
        volatile long expires=System.nanoTime()+LEASE;
        volatile String operation="";
        volatile boolean cancelled=false,transaction=false,readOnly=false;
        Session(Connection c,Artifact a,JsonObject q){connection=c;artifact=a;owner=str(q,"owner");database=str(q,"database");dialect=str(q,"dialect");}
    }

    public static void main(String[] args) throws Exception {
        if(TOKEN==null||TOKEN.length()<32)throw new IllegalStateException("DBHUB_JDBC_TOKEN must contain at least 32 characters");
        Path dir=Path.of(System.getenv().getOrDefault("JDBC_ARTIFACT_DIR","/opt/jdbc/lib")).toRealPath();
        JsonArray manifest=JsonParser.parseString(Files.readString(Path.of(System.getenv().getOrDefault("JDBC_ARTIFACT_LOCK","/opt/jdbc/artifacts.lock.json")))).getAsJsonArray();
        Map<String,Path> verifiedFiles=new HashMap<>();
        for(JsonElement e:manifest){
            JsonObject a=e.getAsJsonObject();String name=str(a,"file");Path file=dir.resolve(name).normalize();
            if(!file.getParent().equals(dir)||!Files.isRegularFile(file))throw new IllegalStateException("invalid driver artifact");
            String hash=HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(Files.readAllBytes(file)));
            if(!hash.equals(str(a,"sha256")))throw new IllegalStateException("artifact checksum mismatch");
            if(verifiedFiles.putIfAbsent(str(a,"id"),file)!=null)throw new IllegalStateException("duplicate artifact ID");
        }
        for(JsonElement e:manifest){
            JsonObject a=e.getAsJsonObject();Path file=verifiedFiles.get(str(a,"id"));String hash=str(a,"sha256");
            String cls=str(a,"class_name");if(cls.isEmpty())continue;
            List<URL> urls=new ArrayList<>();urls.add(file.toUri().toURL());
            if(a.has("dependencies"))for(JsonElement dep:a.getAsJsonArray("dependencies")){Path dependency=verifiedFiles.get(dep.getAsString());if(dependency==null)throw new IllegalStateException("missing locked driver dependency");urls.add(dependency.toUri().toURL());}
            URLClassLoader loader=new URLClassLoader(urls.toArray(URL[]::new),DBHubJdbc.class.getClassLoader());
            Driver driver=(Driver)Class.forName(cls,true,loader).getDeclaredConstructor().newInstance();
            String id=str(a,"id");if(ARTIFACTS.putIfAbsent(id,new Artifact(id,str(a,"name"),str(a,"version"),cls,hash,driver,loader))!=null)throw new IllegalStateException("duplicate artifact");
        }
        System.setProperty("sun.net.httpserver.maxReqTime","20");
        System.setProperty("sun.net.httpserver.maxRspTime","130");
        System.setProperty("sun.net.httpserver.nodelay","true");
        HttpServer server=HttpServer.create(new InetSocketAddress(8090),32);
        server.setExecutor(new ThreadPoolExecutor(8,8,0,TimeUnit.SECONDS,new ArrayBlockingQueue<>(64),daemon("jdbc-http"),new ThreadPoolExecutor.AbortPolicy()));
        server.createContext("/health",x->{if(!x.getRequestMethod().equals("GET")){send(x,405,obj("ok",false));return;}send(x,200,obj("ok",true,"protocol",1,"sessions",SESSIONS.size()));});
        server.createContext("/v1/",DBHubJdbc::handle);
        TIMER.scheduleWithFixedDelay(()->{long now=System.nanoTime();SESSIONS.forEach((id,s)->{if(s.expires<now)close(id,s);});},5,5,TimeUnit.SECONDS);
        Runtime.getRuntime().addShutdownHook(new Thread(()->{server.stop(1);SESSIONS.forEach(DBHubJdbc::close);OPENERS.shutdownNow();TIMER.shutdownNow();ABORT.shutdownNow();}));
        server.start();System.out.println("DBHub JDBC protocol v1 ready; artifacts="+ARTIFACTS.size());
    }

    static void send(HttpExchange x,int status,Object value) throws IOException {byte[] data=JSON.toJson(value).getBytes(StandardCharsets.UTF_8);x.getResponseHeaders().set("Content-Type","application/json; charset=utf-8");x.getResponseHeaders().set("Cache-Control","no-store");x.sendResponseHeaders(status,data.length);try(OutputStream o=x.getResponseBody()){o.write(data);}finally{x.close();}}
    static void handle(HttpExchange x) throws IOException {
        try {
            byte[] supplied=x.getRequestHeaders().getFirst("Authorization")==null?new byte[0]:x.getRequestHeaders().getFirst("Authorization").getBytes(StandardCharsets.UTF_8);
            if(!MessageDigest.isEqual(("Bearer "+TOKEN).getBytes(StandardCharsets.UTF_8),supplied))throw new Problem(401,"authentication","服务身份验证失败");
            if(!x.getRequestMethod().equals("POST"))throw new Problem(405,"invalid_config","仅支持 POST");
            byte[] body=x.getRequestBody().readNBytes(2*1024*1024+1);if(body.length>2*1024*1024)throw new Problem(413,"invalid_config","请求过大");
            JsonObject q=JsonParser.parseString(new String(body,StandardCharsets.UTF_8)).getAsJsonObject();
            String path=x.getRequestURI().getPath();Object result;
            if(path.equals("/v1/artifacts")){List<Object> a=new ArrayList<>();ARTIFACTS.values().forEach(v->a.add(v.summary()));result=obj("protocol",1,"items",a);}
            else if(path.equals("/v1/validate-template")){Artifact a=ARTIFACTS.get(str(q,"artifact_id"));if(a==null||!a.driver.acceptsURL(str(q,"url")))throw new Problem(400,"invalid_config","驱动不接受此 URL 模板");result=obj("accepted",true);}
            else if(path.equals("/v1/open"))result=open(q);
            else {
                String id=str(q,"session_id");Session s=SESSIONS.get(id);
                if(s==null||s.expires<System.nanoTime())throw new Problem(404,"session_expired","目标会话不存在或已过期");
                if(!s.owner.equals(str(q,"owner")))throw new Problem(403,"permission","会话归属不匹配");
                if(path.equals("/v1/close")){close(id,s);result=obj("closed",true);}
                else if(path.equals("/v1/cancel")){if(s.operation.equals(str(q,"execution_key"))){s.cancelled=true;Statement st=s.statement.get();if(st!=null)try{st.cancel();}catch(SQLException ignored){} }result=obj("requested",true,"confirmed_stopped",false);}
                else {
                    if(!s.lock.tryLock())throw new Problem(409,"resource_exhausted","会话已有正在执行的操作");
                    int budget=Math.max(1,Math.min(120000,number(q,"budget_ms",20000)));
                    ScheduledFuture<?> deadline=TIMER.schedule(()->close(id,s),budget,TimeUnit.MILLISECONDS);
                    try {
                        if(SESSIONS.get(id)!=s)throw new Problem(404,"session_expired","目标会话已失效");
                        s.expires=System.nanoTime()+LEASE;
                        result=switch(path){
                            case "/v1/probe" -> probe(s);
                            case "/v1/execute" -> execute(s,q);
                            case "/v1/metadata" -> metadata(s,q);
                            case "/v1/begin" -> {begin(s,bool(q,"read_only"));yield obj("started",true);}
                            case "/v1/commit" -> {if(!s.transaction)throw new Problem(400,"invalid_config","事务未开启");s.connection.commit();reset(s);yield obj("committed",true);}
                            case "/v1/rollback" -> {if(!s.transaction)throw new Problem(400,"invalid_config","事务未开启");s.connection.rollback();reset(s);yield obj("rolled_back",true);}
                            default -> throw new Problem(404,"unsupported","协议操作不存在");
                        };
                    } finally {deadline.cancel(false);s.lock.unlock();}
                }
            }
            send(x,200,obj("data",result));
        } catch(Problem p){send(x,p.status,obj("error",obj("kind",p.kind,"message",p.getMessage(),"sql_state",p.state,"definite",p.definite)));}
        catch(SQLException e){String state=Objects.toString(e.getSQLState(),"");boolean definite=false;send(x,400,obj("error",obj("kind","statement_rejected","message","目标数据库返回错误（"+state+"）","sql_state",state,"definite",definite)));}
        catch(JsonParseException|IllegalArgumentException e){send(x,400,obj("error",obj("kind","invalid_config","message","协议参数无效")));}
        catch(Exception e){send(x,503,obj("error",obj("kind","transport","message","JDBC 服务操作失败")));}
    }

    static Object open(JsonObject q) throws Exception {
        Artifact a=ARTIFACTS.get(str(q,"artifact_id"));if(a==null)throw new Problem(400,"unsupported","驱动制品未安装");
        if(!a.sha256.equals(str(q,"sha256")))throw new Problem(409,"artifact_changed","驱动制品与实例绑定版本不符");
        String url=str(q,"url");if(url.length()>4096||!a.driver.acceptsURL(url))throw new Problem(400,"invalid_config","URL 与驱动不匹配");
        if(str(q,"owner").isEmpty())throw new Problem(400,"invalid_config","缺少会话归属");
        String dialect=str(q,"dialect");if(!dialect.isEmpty()&&!((dialect.equals("postgres")&&Set.of("postgresql","opengauss").contains(a.name))||(dialect.equals("mysql")&&a.name.equals("mysql"))))throw new Problem(400,"unsupported","驱动未实现所选方言");
        if(!SESSION_SLOTS.tryAcquire())throw new Problem(503,"resource_exhausted","JDBC 会话已满");
        int budget=Math.max(100,Math.min(30000,number(q,"budget_ms",10000)));Properties p=new Properties();
        p.setProperty("user",str(q,"username"));p.setProperty("password",str(q,"password"));
        int seconds=Math.max(1,(budget+999)/1000);
        if(Set.of("postgresql","opengauss").contains(a.name)){p.setProperty("connectTimeout",String.valueOf(seconds));p.setProperty("socketTimeout","120");p.setProperty("stringtype","unspecified");p.setProperty("ApplicationName","DBHub JDBC");p.setProperty("sslfactory",a.name.equals("opengauss")?"org.opengauss.ssl.DefaultJavaSSLFactory":"org.postgresql.ssl.DefaultJavaSSLFactory");p.setProperty("sslmode",str(q,"ssl_mode").equals("disable")?"disable":"verify-full");}
        if(a.name.equals("mysql")){p.setProperty("connectTimeout",String.valueOf(budget));p.setProperty("socketTimeout","120000");p.setProperty("autoReconnect","false");p.setProperty("useCursorFetch","true");p.setProperty("useServerPrepStmts","true");p.setProperty("allowPublicKeyRetrieval","true");p.setProperty("sslMode",str(q,"ssl_mode").equals("disable")?"DISABLED":"VERIFY_IDENTITY");}
        AtomicBoolean abandoned=new AtomicBoolean(false);AtomicReference<Connection> opened=new AtomicReference<>();Future<Connection> future=null;
        try {
            future=OPENERS.submit(()->{Connection c=a.driver.connect(url,p);if(c==null)throw new SQLException("driver rejected URL");opened.set(c);if(abandoned.get()){if(opened.compareAndSet(c,null))c.close();throw new SQLException("connection expired");}return c;});
            Connection c=future.get(budget,TimeUnit.MILLISECONDS);
            if(abandoned.get()){c.close();throw new Problem(504,"deadline","建连超时");}
            opened.compareAndSet(c,null);String id=UUID.randomUUID().toString();SESSIONS.put(id,new Session(c,a,q));return obj("session_id",id,"protocol",1,"lease_ms",300000);
        } catch(Exception e){abandoned.set(true);if(future!=null)future.cancel(true);Connection orphan=opened.getAndSet(null);if(orphan!=null){ABORT.execute(()->{try{orphan.close();}catch(Exception ignored){}finally{SESSION_SLOTS.release();}});}else SESSION_SLOTS.release();if(e instanceof RejectedExecutionException)throw new Problem(503,"resource_exhausted","JDBC 建连并发已满");throw connectionFailure(e);}
    }

    // Open failures have no dispatched user SQL. Preserve typed causes and
    // protocol codes for diagnostics without returning vendor text or URLs.
    static Problem connectionFailure(Throwable failure) {
        SQLException sql=null;boolean tls=false,dns=false,timeout=false,network=false;
        Set<Throwable> seen=Collections.newSetFromMap(new IdentityHashMap<>());
        for(Throwable e=failure;e!=null&&seen.size()<32&&seen.add(e);e=e.getCause()) {
            if(e instanceof Problem p)return p;
            if(e instanceof javax.net.ssl.SSLException||e instanceof java.security.cert.CertificateException)tls=true;
            if(e instanceof UnknownHostException)dns=true;
            if(e instanceof TimeoutException||e instanceof SocketTimeoutException||e instanceof SQLTimeoutException)timeout=true;
            if(e instanceof ConnectException||e instanceof NoRouteToHostException||e instanceof SocketException)network=true;
            if(e instanceof SQLException s){sql=s;if(Objects.toString(s.getMessage(),"").startsWith("The server does not support SSL."))tls=true;}
        }
        String state=sql==null?"":Objects.toString(sql.getSQLState(),"");
        if(!state.matches("[A-Z0-9]{5}"))state="";
        int vendor=sql==null?0:sql.getErrorCode();
        String kind="connection_failed",message="目标连接失败，原因尚未确定";
        if(tls||vendor==3159){kind="tls_failed";message="TLS 握手或证书验证失败";}
        else if(timeout){kind="deadline";message="建连超时";}
        else if(state.startsWith("28")||vendor==1045||vendor==1698){kind="authentication_failed";message="数据库身份验证失败";}
        else if(state.equals("42501")||Set.of(1044,1142,1143,1227).contains(vendor)){kind="permission_denied";message="数据库账号权限不足";}
        else if(state.equals("3D000")||vendor==1049){kind="database_not_found";message="目标数据库或服务不存在";}
        else if(state.equals("53300")||vendor==1040||vendor==1203){kind="resource_exhausted";message="数据库连接容量已满";}
        else if(dns){kind="dns_failed";message="无法解析目标主机名";}
        else if(network||state.startsWith("08")){kind="network_unreachable";message="无法连接目标网络端点";}
        return new Problem(kind.equals("deadline")?504:400,kind,message,state,false);
    }
    static void close(String id,Session s){if(!SESSIONS.remove(id,s))return;s.cancelled=true;ABORT.execute(()->{try{try{s.connection.abort(Runnable::run);}catch(Exception ignored){}try{s.connection.close();}catch(Exception ignored){}}finally{SESSION_SLOTS.release();}});}

    static Object probe(Session s) throws SQLException {DatabaseMetaData m=s.connection.getMetaData();String product=m.getDatabaseProductName(),version=m.getDatabaseProductVersion();return obj("type","jdbc","version",version,"compatibility",obj("engine",product.toLowerCase(Locale.ROOT),"product",product,"version",version,"status","unverified","message","JDBC 连接成功；原生驱动兼容矩阵不代表此 JDBC 接入已完整验证。"),"driver_version",m.getDriverVersion());}
    static void begin(Session s,boolean readOnly) throws SQLException {
        if(s.transaction)throw new Problem(409,"invalid_config","事务已开启");
        if(!Set.of("mysql","postgres").contains(s.dialect))throw new Problem(400,"unsupported","该产品尚未启用事务");
        s.connection.setReadOnly(readOnly);s.connection.setAutoCommit(false);
        if(readOnly)try(Statement st=s.connection.createStatement()){st.execute("SET TRANSACTION READ ONLY");}
        s.transaction=true;s.readOnly=readOnly;
    }
    static void reset(Session s) throws SQLException {s.transaction=false;s.readOnly=false;s.connection.setAutoCommit(true);s.connection.setReadOnly(false);}
    static Object execute(Session s,JsonObject q) throws SQLException {
        String key=str(q,"execution_key");if(key.isEmpty()||key.length()>200)throw new Problem(400,"invalid_config","执行键无效");
        if(s.executed.size()>=1000)throw new Problem(503,"resource_exhausted","会话执行次数已满，请结束会话");
        if(!s.executed.add(key))throw new Problem(409,"duplicate","该执行键已接收，不会重复执行");
        String sql=str(q,"sql");if(sql.isBlank()||sql.length()>1024*1024)throw new Problem(400,"invalid_config","SQL 无效");
        boolean ro=bool(q,"read_only"),own=(ro||bool(q,"cursor"))&&!s.transaction;
        if(ro&&s.transaction&&!s.readOnly)throw new Problem(400,"permission","只读请求不能使用读写事务");
        if(own)begin(s,ro);
        s.operation=key;s.cancelled=false;long start=System.nanoTime();
        int max=Math.max(1,Math.min(10000,number(q,"max_rows",1000)));int byteBudget=number(q,"max_bytes",0);byteBudget=byteBudget<=0?8*1024*1024:Math.min(8*1024*1024,byteBudget-4096);boolean succeeded=false;
        try(PreparedStatement st=s.connection.prepareStatement(sql)){
            s.statement.set(st);st.setMaxRows(max+1);st.setFetchSize(Math.min(max+1,100));st.setQueryTimeout(Math.max(1,(number(q,"budget_ms",30000)+999)/1000));
            JsonArray args=q.has("args")?q.getAsJsonArray("args"):new JsonArray();int index=1;
            for(JsonElement arg:args){JsonObject v=arg.getAsJsonObject();String kind=str(v,"kind"),text=str(v,"text");switch(kind){case "null"->st.setObject(index,null);case "boolean"->st.setBoolean(index,bool(v,"value"));case "integer"->st.setLong(index,Long.parseLong(text));case "decimal"->st.setBigDecimal(index,new java.math.BigDecimal(text));case "string"->st.setString(index,text);default->throw new Problem(400,"invalid_config","参数类型不支持");}index++;}
            if(s.cancelled)throw new Problem(409,"canceled","请求已取消，执行结果需核实");
            boolean query=st.execute();List<String> columns=new ArrayList<>();List<Object> rows=new ArrayList<>(),types=new ArrayList<>();long affected=0;boolean truncated=false;
            if(query)try(ResultSet rs=st.getResultSet()){
                ResultSetMetaData m=rs.getMetaData();for(int i=1;i<=m.getColumnCount();i++){columns.add(m.getColumnLabel(i));types.add(obj("name",m.getColumnLabel(i),"database_type",m.getColumnTypeName(i),"precision",m.getPrecision(i),"scale",m.getScale(i)));}
                int bytes=0;
                while(rs.next()){
                    if(rows.size()>=max){truncated=true;break;}
                    List<Object> row=new ArrayList<>();for(int i=1;i<=m.getColumnCount();i++)row.add(value(s,rs,m,i));
                    int size=JSON.toJson(row).getBytes(StandardCharsets.UTF_8).length;if(size>byteBudget-bytes){throw new Problem(413,"resource_exhausted","查询结果超出字节预算");}bytes+=size;rows.add(row);
                }
            }else affected=Math.max(0,st.getLargeUpdateCount());
            succeeded=true;return obj("kind",query?"query":"write","columns",columns,"rows",rows,"column_types",types,"affected_rows",affected,"duration_ms",TimeUnit.NANOSECONDS.toMillis(System.nanoTime()-start),"truncated",truncated);
        }catch(SQLException e){String state=Objects.toString(e.getSQLState(),"");boolean definite=s.artifact.name.equals("postgresql")&&state.length()==5&&!state.equals("40003")&&Set.of("22","23","25","40","42","0A").contains(state.substring(0,2));throw new Problem(400,"statement_rejected","目标数据库返回错误（"+state+"）",state,definite);
        }finally{
            s.statement.set(null);s.operation="";
            if(own){if(succeeded&&!ro)s.connection.commit();else s.connection.rollback();reset(s);}
        }
    }
    static Object value(Session s,ResultSet rs,ResultSetMetaData m,int i) throws SQLException {
        int type=m.getColumnType(i);
        switch(type){
            case Types.BINARY:case Types.VARBINARY:case Types.LONGVARBINARY:case Types.BLOB:
                try(InputStream stream=rs.getBinaryStream(i)){if(stream==null)return obj("kind","null");byte[] bytes=stream.readNBytes(1024*1024+1);if(bytes.length>1024*1024)throw new Problem(413,"resource_exhausted","单个二进制值超出 1 MiB 上限");return obj("kind","binary","text",Base64.getEncoder().encodeToString(bytes));}catch(IOException e){throw new SQLException("value stream failed","HY000");}
            case Types.BIT:
                if(m.getColumnTypeName(i).equalsIgnoreCase("bit")&&m.getPrecision(i)>1){String bits;if(s.artifact.name.equals("mysql")){byte[] raw=rs.getBytes(i);if(raw==null)return obj("kind","null");bits=new java.math.BigInteger(1,raw).toString(2);bits="0".repeat(Math.max(0,m.getPrecision(i)-bits.length()))+bits;}else bits=rs.getString(i);return bits==null?obj("kind","null"):obj("kind","bit_string","text",bits);}
            case Types.BOOLEAN:boolean flag=rs.getBoolean(i);return rs.wasNull()?obj("kind","null"):obj("kind","boolean","value",flag);
            case Types.TINYINT:case Types.SMALLINT:case Types.INTEGER:case Types.BIGINT:String integer=rs.getString(i);return integer==null?obj("kind","null"):obj("kind","integer","text",integer);
            case Types.NUMERIC:case Types.DECIMAL:java.math.BigDecimal decimal=rs.getBigDecimal(i);return decimal==null?obj("kind","null"):obj("kind","decimal","text",decimal.toPlainString());
            case Types.REAL:case Types.FLOAT:case Types.DOUBLE:double value=rs.getDouble(i);return rs.wasNull()?obj("kind","null"):obj("kind",Double.isFinite(value)?"float":"string","text",Double.toString(value));
            case Types.DATE:String date=rs.getString(i);return date==null?obj("kind","null"):obj("kind","date","text",date);
            case Types.TIME:case Types.TIME_WITH_TIMEZONE:String time=rs.getString(i);return time==null?obj("kind","null"):obj("kind","time","text",time);
            case Types.TIMESTAMP:String timestamp=rs.getString(i);return timestamp==null?obj("kind","null"):obj("kind","timestamp","text",timestamp);
            case Types.TIMESTAMP_WITH_TIMEZONE:OffsetDateTime offset=rs.getObject(i,OffsetDateTime.class);return offset==null?obj("kind","null"):obj("kind","timestamp_with_timezone","text",offset.toString());
            default:
                try(Reader reader=rs.getCharacterStream(i)){if(reader==null)return obj("kind","null");StringBuilder text=new StringBuilder();char[] chunk=new char[8192];int n;while((n=reader.read(chunk))!=-1){if(text.length()+n>1024*1024)throw new Problem(413,"resource_exhausted","单个文本值超出 1 Mi 字符上限");text.append(chunk,0,n);}String kind=Set.of("json","jsonb").contains(m.getColumnTypeName(i).toLowerCase(Locale.ROOT))?"json":"string";return obj("kind",kind,"text",text.toString());}catch(IOException e){throw new SQLException("value stream failed","HY000");}
        }
    }

    static String pattern(DatabaseMetaData m,String value) throws SQLException {String e=m.getSearchStringEscape();if(e==null||e.isEmpty())return value;return value.replace(e,e+e).replace("%",e+"%").replace("_",e+"_");}
    static String schema(JsonObject q){String s=str(q,"schema");return s.isEmpty()?null:s;}
    static boolean userSchema(String n){String s=n.toLowerCase(Locale.ROOT);return !s.equals("information_schema")&&!s.equals("pg_catalog")&&!s.startsWith("pg_toast");}
    static Object metadata(Session s,JsonObject q) throws SQLException {
        DatabaseMetaData m=s.connection.getMetaData();String operation=str(q,"operation");String catalog=s.connection.getCatalog();String schema=schema(q);String table=str(q,"table");List<Object> result=new ArrayList<>();
        switch(operation){
            case "databases":return List.of(obj("name",s.database.isEmpty()?Objects.toString(catalog,""):s.database));
            case "schemas":
                if(s.artifact.name.equals("mysql"))return List.of(Objects.toString(catalog,s.database));
                try(ResultSet rs=m.getSchemas()){while(rs.next()){String name=rs.getString("TABLE_SCHEM");if(name!=null&&userSchema(name))result.add(name);if(result.size()>5000)throw new Problem(413,"resource_exhausted","Schema 数量超限");}}return result;
            case "tables":
                try(ResultSet rs=m.getTables(catalog,schema==null?null:pattern(m,schema),"%",new String[]{"TABLE","VIEW","MATERIALIZED VIEW"})){while(rs.next()){String name=rs.getString("TABLE_NAME"),kind=rs.getString("TABLE_TYPE");result.add(obj("name",name,"type",kind!=null&&kind.contains("VIEW")?"view":"table","comment",Objects.toString(rs.getString("REMARKS"),"")));if(result.size()>5000)throw new Problem(413,"resource_exhausted","表数量超限，未完成清单读取");}}return result;
            case "columns":return columns(m,catalog,schema,table);
            case "describe":
                Map<String,Object> status=obj("ddl","unsupported","statistics","unsupported");List<Object> indexes=new ArrayList<>(),keys=new ArrayList<>();Map<String,SortedMap<Integer,String>> pks=new LinkedHashMap<>();
                try(ResultSet rs=m.getPrimaryKeys(catalog,schema,table)){while(rs.next()){String name=Objects.toString(rs.getString("PK_NAME"),"PRIMARY");pks.computeIfAbsent(name,k->new TreeMap<>()).put(rs.getInt("KEY_SEQ"),rs.getString("COLUMN_NAME"));}}
                pks.forEach((name,cols)->keys.add(obj("name",name,"kind","primary_key","columns",new ArrayList<>(cols.values()),"ref_schema","","ref_table","","ref_columns",List.of())));
                Map<String,Map<String,Object>> idx=new LinkedHashMap<>();
                try(ResultSet rs=m.getIndexInfo(catalog,schema,table,false,true)){while(rs.next()){String name=rs.getString("INDEX_NAME"),column=rs.getString("COLUMN_NAME");if(name==null||column==null)continue;Map<String,Object> item=idx.get(name);if(item==null){item=obj("name",name,"columns",new ArrayList<String>(),"is_unique",!rs.getBoolean("NON_UNIQUE"),"is_primary",pks.containsKey(name));idx.put(name,item);}((List<String>)item.get("columns")).add(column);}}catch(SQLFeatureNotSupportedException ex){status.put("indexes","unsupported");}indexes.addAll(idx.values());
                Map<String,Map<String,Object>> foreign=new LinkedHashMap<>();
                try(ResultSet rs=m.getImportedKeys(catalog,schema,table)){while(rs.next()){String name=Objects.toString(rs.getString("FK_NAME"),"FOREIGN");Map<String,Object> item=foreign.get(name);if(item==null){item=obj("name",name,"kind","foreign_key","columns",new ArrayList<String>(),"ref_schema",Objects.toString(rs.getString(s.artifact.name.equals("mysql")?"PKTABLE_CAT":"PKTABLE_SCHEM"),""),"ref_table",rs.getString("PKTABLE_NAME"),"ref_columns",new ArrayList<String>());foreign.put(name,item);}((List<String>)item.get("columns")).add(rs.getString("FKCOLUMN_NAME"));((List<String>)item.get("ref_columns")).add(rs.getString("PKCOLUMN_NAME"));}}catch(SQLFeatureNotSupportedException ex){status.put("foreign_keys","unsupported");}keys.addAll(foreign.values());
                return obj("name",table,"schema",Objects.toString(schema,""),"type",str(q,"table_type").equals("view")?"view":"table","comment","","estimated_rows",0,"data_bytes",0,"columns",columns(m,catalog,schema,table),"indexes",indexes,"keys",keys,"ddl","","metadata_status",status);
            default:throw new Problem(400,"unsupported","元数据操作不支持");
        }
    }
    static List<Object> columns(DatabaseMetaData m,String catalog,String schema,String table) throws SQLException {
        if(table.isEmpty())throw new Problem(400,"invalid_config","表名必填");Set<String> primary=new HashSet<>();try(ResultSet pk=m.getPrimaryKeys(catalog,schema,table)){while(pk.next())primary.add(pk.getString("COLUMN_NAME"));}
        List<Object> out=new ArrayList<>();try(ResultSet rs=m.getColumns(catalog,schema==null?null:pattern(m,schema),pattern(m,table),"%")){while(rs.next()){String name=rs.getString("COLUMN_NAME");out.add(obj("name",name,"data_type",rs.getString("TYPE_NAME"),"is_primary",primary.contains(name),"is_nullable",rs.getInt("NULLABLE")!=DatabaseMetaData.columnNoNulls,"ordinal",rs.getInt("ORDINAL_POSITION"),"comment",Objects.toString(rs.getString("REMARKS"),""),"default",Objects.toString(rs.getString("COLUMN_DEF"),"")));if(out.size()>10000)throw new Problem(413,"resource_exhausted","字段数量超限");}}return out;
    }
}
