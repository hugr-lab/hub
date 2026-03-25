import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;

/** Check that the endpoint returns HTTP 200.
 *  Run:  java HealthCheck.java http://localhost:9000/health/ready */
public class HealthCheck {
    public static void main(String[] args) throws Exception {
        if (args.length == 0) System.exit(1);

        URI uri = URI.create(args[0]);
        HttpClient client = HttpClient.newBuilder()
                .connectTimeout(Duration.ofSeconds(2))
                .build();

        HttpRequest req = HttpRequest.newBuilder(uri)
                .timeout(Duration.ofSeconds(2))
                .GET()
                .build();

        int code = client.send(req, HttpResponse.BodyHandlers.discarding())
                         .statusCode();

        System.exit(code == 200 ? 0 : 1);
    }
}