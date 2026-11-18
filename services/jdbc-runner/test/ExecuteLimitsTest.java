import com.google.gson.*;
import java.net.URI;
import java.net.http.*;
import java.time.Duration;
import java.util.UUID;

/** 对独立 PostgreSQL 夹具执行真实 JDBC HTTP 协议检查，不使用业务数据库。 */
public final class ExecuteLimitsTest {
    private static final HttpClient HTTP = HttpClient.newHttpClient();
    private static final String BASE = required("DBHUB_JDBC_TEST_URL");
    private static final String TOKEN = required("DBHUB_JDBC_TOKEN");
    private static final String OWNER = "jdbc-directed-" + UUID.randomUUID();
    private static String session;
    private static int checks;

    private static String required(String name) {
        String value = System.getenv(name);
        if (value == null || value.isBlank()) throw new IllegalArgumentException(name + " is required");
        return value;
    }

    private static JsonObject call(String path, JsonObject body, int expected) throws Exception {
        HttpRequest request = HttpRequest.newBuilder(URI.create(BASE + "/v1/" + path))
                .timeout(Duration.ofSeconds(40)).header("Authorization", "Bearer " + TOKEN)
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(body.toString())).build();
        HttpResponse<String> response = HTTP.send(request, HttpResponse.BodyHandlers.ofString());
        if (response.statusCode() != expected)
            throw new AssertionError(path + ": expected HTTP " + expected + ", got " + response.statusCode());
        JsonObject result = JsonParser.parseString(response.body()).getAsJsonObject();
        return result.getAsJsonObject(expected < 300 ? "data" : "error");
    }

    private static JsonObject execute(String sql, Integer maxRows, boolean large, int bytes, int status) throws Exception {
        JsonObject q = new JsonObject();
        q.addProperty("owner", OWNER);
        q.addProperty("session_id", session);
        q.addProperty("execution_key", UUID.randomUUID().toString());
        q.addProperty("sql", sql);
        q.addProperty("read_only", true);
        q.addProperty("cursor", true);
        q.addProperty("large_result", large);
        q.addProperty("max_bytes", bytes);
        q.addProperty("budget_ms", 30000);
        if (maxRows != null) q.addProperty("max_rows", maxRows);
        return call("execute", q, status);
    }

    private static void check(boolean ok, String name) {
        if (!ok) throw new AssertionError(name);
        checks++;
        System.out.println("PASS " + name);
    }

    private static void rows(JsonObject result, int count, boolean truncated, String name) {
        check(result.getAsJsonArray("rows").size() == count
                && result.get("truncated").getAsBoolean() == truncated, name);
    }

    public static void main(String[] args) throws Exception {
        JsonArray artifacts = call("artifacts", new JsonObject(), 200).getAsJsonArray("items");
        JsonObject driver = null;
        for (JsonElement a : artifacts)
            if (a.getAsJsonObject().get("name").getAsString().equals("postgresql")) driver = a.getAsJsonObject();
        if (driver == null) throw new AssertionError("locked PostgreSQL driver missing");
        JsonObject open = new JsonObject();
        open.addProperty("owner", OWNER);
        open.add("artifact_id", driver.get("id"));
        open.add("sha256", driver.get("sha256"));
        open.addProperty("url", required("DBHUB_JDBC_TEST_DATABASE_URL"));
        open.addProperty("username", required("DBHUB_JDBC_TEST_USER"));
        open.addProperty("password", required("DBHUB_JDBC_TEST_PASSWORD"));
        open.addProperty("database", required("DBHUB_JDBC_TEST_DATABASE"));
        open.addProperty("dialect", "postgres");
        open.addProperty("ssl_mode", "disable");
        session = call("open", open, 200).get("session_id").getAsString();
        try {
            rows(execute("SELECT i FROM generate_series(1,1001) i", null, false, 0, 200), 1000, true, "default 1000 rows");
            rows(execute("SELECT i FROM generate_series(1,10001) i", 100000, false, 0, 200), 10000, true, "ordinary query capped at 10000");
            rows(execute("SELECT i FROM generate_series(1,10000) i", 10000, false, 0, 200), 10000, false, "exact row boundary is complete");
            rows(execute("SELECT i FROM generate_series(1,100001) i", 200000, true, 16 * 1024 * 1024, 200), 100000, true, "large result capped at 100000");
            String nineMiB = "SELECT repeat('x',900000) FROM generate_series(1,10)";
            check(execute(nineMiB, 100, false, 16 * 1024 * 1024, 413).get("kind").getAsString().equals("resource_exhausted"), "ordinary result retains 8 MiB budget");
            rows(execute(nineMiB, 100, true, 16 * 1024 * 1024, 200), 10, false, "large result accepts more than 8 MiB");
            check(execute("SELECT repeat('x',900000) FROM generate_series(1,20)", 100, true, 32 * 1024 * 1024, 413).get("kind").getAsString().equals("resource_exhausted"), "large result retains 16 MiB ceiling");
            JsonObject readonly = execute("CREATE TABLE public.jdbc_forbidden_write(id integer)", 1, false, 0, 400);
            check(readonly.get("sql_state").getAsString().equals("25006") && readonly.get("definite").getAsBoolean(), "read-only transaction rejects writes definitively");
            JsonObject rejected = execute("SELECT FROM", 1, false, 0, 400);
            check(rejected.get("sql_state").getAsString().equals("42601") && rejected.get("definite").getAsBoolean(), "statement error preserves SQL state and certainty");
            JsonArray row = execute("SELECT 12345678901234567890.12345678::numeric,9223372036854775807::bigint,NULL::text,current_setting('transaction_read_only'),to_regclass('public.jdbc_forbidden_write')", 1, false, 0, 200)
                    .getAsJsonArray("rows").get(0).getAsJsonArray();
            check(row.get(0).getAsJsonObject().get("text").getAsString().equals("12345678901234567890.12345678")
                    && row.get(1).getAsJsonObject().get("text").getAsString().equals("9223372036854775807")
                    && row.get(2).getAsJsonObject().get("kind").getAsString().equals("null")
                    && row.get(3).getAsJsonObject().get("text").getAsString().equals("on")
                    && row.get(4).getAsJsonObject().get("kind").getAsString().equals("null"),
                    "session recovers after errors and preserves numeric precision, NULL and read-only mode");
        } finally {
            JsonObject close = new JsonObject();
            close.addProperty("owner", OWNER);
            close.addProperty("session_id", session);
            call("close", close, 200);
        }
        System.out.println("PASS JDBC execution integration: " + checks + " checks");
    }
}
