import java.net.*;
import java.sql.*;
import java.util.concurrent.*;
import javax.net.ssl.SSLHandshakeException;

public final class ConnectionDiagnosticsTest {
    static int checks;
    static void check(String kind, Throwable cause) {
        DBHubJdbc.Problem p=DBHubJdbc.connectionFailure(new ExecutionException(cause));
        if(!p.kind.equals(kind)||p.definite||p.getMessage().contains("private-fixture"))throw new AssertionError("unsafe or incorrect connection classification: "+kind);
        checks++;
    }
    public static void main(String[] args) {
        check("authentication_failed",new SQLException("private-fixture","28P01"));
        check("authentication_failed",new SQLException("private-fixture","28000",1045));
        check("permission_denied",new SQLException("private-fixture","42501"));
        check("permission_denied",new SQLException("private-fixture","42000",1044));
        check("database_not_found",new SQLException("private-fixture","42000",1049));
        check("database_not_found",new SQLException("private-fixture","3D000"));
        check("resource_exhausted",new SQLException("private-fixture","53300"));
        check("tls_failed",new SQLException("private-fixture","08001",new SSLHandshakeException("private-fixture")));
        check("dns_failed",new SQLException("private-fixture","08001",new UnknownHostException("private-fixture")));
        check("network_unreachable",new SQLException("private-fixture","08001",new ConnectException("private-fixture")));
        check("deadline",new SocketTimeoutException("private-fixture"));
        check("connection_failed",new IllegalStateException("private-fixture"));
        DBHubJdbc.Problem untrusted=DBHubJdbc.connectionFailure(new SQLException("private-fixture","private-fixture"));
        if(!untrusted.state.isEmpty())throw new AssertionError("unsanitized vendor SQL state");
        System.out.println("PASS JDBC connection diagnostics: "+checks+" categories/causes; unknown state sanitized");
    }
}
