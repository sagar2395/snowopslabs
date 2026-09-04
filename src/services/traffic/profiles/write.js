// Write profile — exercises a WRITE path with a JSON body, not just reads.
// Good against echo-server's /echo (set the target app to echo-server in the
// UI, or pass --target http://echo-server.echo-server.svc.cluster.local:8080/).
// TRAFFIC_TARGET is treated as a BASE origin; TRAFFIC_METHOD overrides the verb.
import http from 'k6/http';
import { check } from 'k6';

const base = (__ENV.TRAFFIC_TARGET || 'http://echo-server.echo-server.svc.cluster.local:8080/')
  .replace(/(https?:\/\/[^/]+).*/, '$1');
const method = (__ENV.TRAFFIC_METHOD || 'POST').toUpperCase();
const rps = Number(__ENV.TRAFFIC_RPS || 10);
const duration = __ENV.TRAFFIC_DURATION || '10m';

export const options = {
  scenarios: {
    write: {
      executor: 'constant-arrival-rate',
      rate: rps,
      timeUnit: '1s',
      duration: duration,
      preAllocatedVUs: Math.max(10, rps * 2),
      maxVUs: Math.max(20, rps * 4),
    },
  },
  thresholds: {
    http_req_failed: [{ threshold: 'rate<0.05', abortOnFail: false }],
  },
};

export default function () {
  const url = `${base}/echo`;
  const payload = JSON.stringify({ ts: Date.now(), vu: __VU, iter: __ITER });
  const params = { headers: { 'Content-Type': 'application/json' } };
  const res = http.request(method, url, payload, params);
  check(res, { 'status is 2xx': (r) => r.status >= 200 && r.status < 300 });
}
