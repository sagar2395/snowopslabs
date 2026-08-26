// Soak profile — sustained moderate load over a long period, for finding
// leaks, OOM creep, and slow degradation. Defaults to 2 hours.
import http from 'k6/http';
import { check } from 'k6';

const target = __ENV.TRAFFIC_TARGET;
const rps = Number(__ENV.TRAFFIC_RPS || 5);
const duration = __ENV.TRAFFIC_DURATION || '2h';

export const options = {
  scenarios: {
    soak: {
      executor: 'constant-arrival-rate',
      rate: rps,
      timeUnit: '1s',
      duration: duration,
      preAllocatedVUs: Math.max(10, rps * 2),
      maxVUs: Math.max(20, rps * 4),
    },
  },
};

export default function () {
  const res = http.get(target);
  check(res, { 'status is 2xx/3xx': (r) => r.status >= 200 && r.status < 400 });
}
