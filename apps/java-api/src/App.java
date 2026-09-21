// SPDX-License-Identifier: Apache-2.0
//
// java-api — a second technology stack for SnowOps Labs.
//
// It exists to prove the workload contract is not Go-shaped: the same scenarios
// and faults run against this application unchanged, and its JVM behaviour (slow
// start, GC pauses, a much larger memory floor) is exactly what makes a
// cross-stack comparison worth doing.
//
// Deliberately dependency-free: the JDK's own HTTP server plus a hand-written
// Prometheus exposition. A real service would use Micrometer — README.md gives
// the mapping — but a build that downloads a dependency tree would make the lab
// slow and online-only, and none of that is what the contract is about.

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpHandler;
import com.sun.net.httpserver.HttpServer;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.DoubleAdder;
import java.util.concurrent.atomic.LongAdder;

public final class App {

    private static final String SERVICE = env("APP_NAME", "java-api");
    private static final int PORT = Integer.parseInt(env("PORT", "8080"));
    private static final String VERSION = env("APP_VERSION", "dev");
    private static final int SHUTDOWN_GRACE_SECONDS =
            Integer.parseInt(env("SHUTDOWN_GRACE_SECONDS", "10"));

    /** Flipped by /toggle-failure, which is what backs the readiness-toggle capability. */
    private static final AtomicBoolean SIMULATE_FAILURE = new AtomicBoolean(false);

    private static final Metrics METRICS = new Metrics();
    private static final long STARTED_AT = System.currentTimeMillis();

    public static void main(String[] args) throws IOException {
        HttpServer server = HttpServer.create(new InetSocketAddress(PORT), 0);
        server.setExecutor(Executors.newVirtualThreadPerTaskExecutor());

        server.createContext("/health", instrument("/health",
                ex -> respond(ex, 200, "{\"status\":\"ok\"}")));
        server.createContext("/ready", instrument("/ready", App::handleReady));
        server.createContext("/toggle-failure", instrument("/toggle-failure", App::handleToggle));
        server.createContext("/version", instrument("/version", App::handleVersion));
        // /metrics is served outside the instrumentation, so scraping does not
        // inflate the very series being scraped.
        server.createContext("/metrics", App::handleMetrics);
        server.createContext("/", instrument("/", App::handleRoot));

        // SIGTERM arrives on every rollout, drain and eviction the lab performs.
        // A workload that dies instantly makes all of those look like outages.
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            log("info", "shutting down", "graceSeconds", String.valueOf(SHUTDOWN_GRACE_SECONDS));
            server.stop(SHUTDOWN_GRACE_SECONDS);
        }));

        log("info", "listening", "port", String.valueOf(PORT));
        server.start();
    }

    private static void handleReady(HttpExchange ex) throws IOException {
        if (SIMULATE_FAILURE.get()) {
            respond(ex, 503, "{\"status\":\"not ready\",\"reason\":\"simulated failure\"}");
            return;
        }
        respond(ex, 200, "{\"status\":\"ready\"}");
    }

    private static void handleToggle(HttpExchange ex) throws IOException {
        // AtomicBoolean has no updateAndGet, so toggle with a compare-and-set
        // loop rather than a get/set pair that two callers could interleave.
        boolean now;
        do {
            now = !SIMULATE_FAILURE.get();
        } while (!SIMULATE_FAILURE.compareAndSet(!now, now));
        respond(ex, 200, "{\"message\":\"Readiness failure simulation toggled. Hit /ready to test.\""
                + ",\"simulateFailure\":\"" + now + "\"}");
    }

    private static void handleVersion(HttpExchange ex) throws IOException {
        respond(ex, 200, "{\"service\":\"" + SERVICE + "\",\"version\":\"" + VERSION + "\"}");
    }

    private static void handleRoot(HttpExchange ex) throws IOException {
        long uptime = (System.currentTimeMillis() - STARTED_AT) / 1000;
        respond(ex, 200, "{\"service\":\"" + SERVICE + "\",\"version\":\"" + VERSION
                + "\",\"uptimeSeconds\":" + uptime
                + ",\"runtime\":\"java-" + System.getProperty("java.version") + "\"}");
    }

    private static void handleMetrics(HttpExchange ex) throws IOException {
        byte[] body = METRICS.expose().getBytes(StandardCharsets.UTF_8);
        ex.getResponseHeaders().set("Content-Type", "text/plain; version=0.0.4; charset=utf-8");
        ex.sendResponseHeaders(200, body.length);
        try (OutputStream os = ex.getResponseBody()) {
            os.write(body);
        }
    }

    /** Wraps a handler so every request lands in the histogram. */
    private static HttpHandler instrument(String route, Handler next) {
        return ex -> {
            long start = System.nanoTime();
            try {
                next.handle(ex);
            } catch (IOException | RuntimeException e) {
                log("error", "handler failed", "route", route, "error", String.valueOf(e));
                respond(ex, 500, "{\"error\":\"internal\"}");
            } finally {
                int status = ex.getResponseCode() > 0 ? ex.getResponseCode() : 500;
                METRICS.observe(ex.getRequestMethod(), route, status,
                        (System.nanoTime() - start) / 1_000_000_000.0);
            }
        };
    }

    @FunctionalInterface
    private interface Handler {
        void handle(HttpExchange ex) throws IOException;
    }

    private static void respond(HttpExchange ex, int status, String body) throws IOException {
        byte[] out = body.getBytes(StandardCharsets.UTF_8);
        ex.getResponseHeaders().set("Content-Type", "application/json");
        ex.sendResponseHeaders(status, out.length);
        try (OutputStream os = ex.getResponseBody()) {
            os.write(out);
        }
    }

    /**
     * The one metric the contract requires, in OpenTelemetry semantic
     * conventions: http.server.request.duration, exposed in Prometheus form as
     * http_server_request_duration_seconds.
     *
     * Semconv defines no request counter — the histogram's _count series is the
     * count — so the status code is a label here. That is what lets an error rate
     * be derived from the same series.
     */
    static final class Metrics {
        /** The semconv-recommended bucket boundaries, in seconds. */
        private static final double[] BUCKETS =
                {.005, .01, .025, .05, .075, .1, .25, .5, .75, 1, 2.5, 5, 7.5, 10};

        private final Map<String, Series> series = new ConcurrentHashMap<>();

        void observe(String method, String route, int status, double seconds) {
            series.computeIfAbsent(method + " " + route + " " + status,
                    k -> new Series(method, route, status)).observe(seconds);
        }

        String expose() {
            StringBuilder sb = new StringBuilder(1024);
            sb.append("# HELP http_server_request_duration_seconds ")
              .append("Duration of HTTP server requests in seconds.\n");
            sb.append("# TYPE http_server_request_duration_seconds histogram\n");
            for (Series s : series.values()) {
                s.render(sb);
            }
            return sb.toString();
        }

        private static final class Series {
            private final String method;
            private final String route;
            private final int status;
            // Prometheus histogram buckets are cumulative, so an observation
            // increments every bucket whose boundary it falls at or below.
            private final LongAdder[] buckets = new LongAdder[BUCKETS.length];
            private final LongAdder count = new LongAdder();
            private final DoubleAdder sum = new DoubleAdder();

            Series(String method, String route, int status) {
                this.method = method;
                this.route = route;
                this.status = status;
                for (int i = 0; i < buckets.length; i++) {
                    buckets[i] = new LongAdder();
                }
            }

            void observe(double seconds) {
                count.increment();
                sum.add(seconds);
                for (int i = 0; i < BUCKETS.length; i++) {
                    if (seconds <= BUCKETS[i]) {
                        buckets[i].increment();
                    }
                }
            }

            void render(StringBuilder sb) {
                String labels = "app=\"" + esc(SERVICE)
                        + "\",http_request_method=\"" + esc(method)
                        + "\",http_response_status_code=\"" + status
                        + "\",http_route=\"" + esc(route) + "\"";
                for (int i = 0; i < BUCKETS.length; i++) {
                    sb.append("http_server_request_duration_seconds_bucket{").append(labels)
                      .append(",le=\"").append(trim(BUCKETS[i])).append("\"} ")
                      .append(buckets[i].sum()).append('\n');
                }
                long n = count.sum();
                sb.append("http_server_request_duration_seconds_bucket{").append(labels)
                  .append(",le=\"+Inf\"} ").append(n).append('\n');
                sb.append("http_server_request_duration_seconds_sum{").append(labels).append("} ")
                  .append(sum.sum()).append('\n');
                sb.append("http_server_request_duration_seconds_count{").append(labels).append("} ")
                  .append(n).append('\n');
            }
        }

        private static String trim(double d) {
            return d == Math.rint(d) ? String.valueOf((long) d) : String.valueOf(d);
        }

        private static String esc(String s) {
            return s.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", "\\n");
        }
    }

    private static void log(String level, String msg, String... kv) {
        StringBuilder sb = new StringBuilder("{\"level\":\"").append(level)
                .append("\",\"service\":\"").append(SERVICE)
                .append("\",\"msg\":\"").append(msg).append('"');
        for (int i = 0; i + 1 < kv.length; i += 2) {
            sb.append(",\"").append(kv[i]).append("\":\"").append(kv[i + 1]).append('"');
        }
        System.out.println(sb.append('}'));
    }

    private static String env(String key, String def) {
        String v = System.getenv(key);
        return v == null || v.isBlank() ? def : v;
    }

    private App() {
    }
}
