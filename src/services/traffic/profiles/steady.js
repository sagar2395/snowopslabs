// Steady profile — constant request rate for the whole duration.
// Tuned via env: TRAFFIC_TARGET, TRAFFIC_RPS, TRAFFIC_DURATION.
import http from 'k6/http';
import { check } from 'k6';

const target = __ENV.TRAFFIC_TARGET;
const rps = Number(__ENV.TRAFFIC_RPS || 10);
const duration = __ENV.TRAFFIC_DURATION || '10m';

export const options = {
  scenarios: {
    steady: {
      executor: 'constant-arrival-rate',
      rate: rps,
      timeUnit: '1s',
      duration: duration,
      preAllocatedVUs: Math.max(10, rps * 2),
      maxVUs: Math.max(20, rps * 4),
    },
  },
  thresholds: {
    // Informational; the run itself never aborts on threshold breach.
    http_req_failed: [{ threshold: 'rate<0.05', abortOnFail: false }],
  },
};

export default function () {
  const res = http.get(target);
  check(res, { 'status is 2xx/3xx': (r) => r.status >= 200 && r.status < 400 });
}
