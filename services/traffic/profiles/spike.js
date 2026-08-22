// Spike profile — baseline load, a sharp 10x spike, then recovery.
// TRAFFIC_RPS sets the baseline; the spike is 10x that. TRAFFIC_DURATION
// is ignored (the shape is fixed: ~6 minutes total) so the spike stays sharp.
import http from 'k6/http';
import { check } from 'k6';

const target = __ENV.TRAFFIC_TARGET;
const rps = Number(__ENV.TRAFFIC_RPS || 10);

export const options = {
  scenarios: {
    spike: {
      executor: 'ramping-arrival-rate',
      startRate: rps,
      timeUnit: '1s',
      preAllocatedVUs: Math.max(20, rps * 4),
      maxVUs: Math.max(100, rps * 20),
      stages: [
        { duration: '1m', target: rps },        // baseline
        { duration: '30s', target: rps * 10 },  // ramp up
        { duration: '2m', target: rps * 10 },   // hold the spike
        { duration: '30s', target: rps },       // ramp down
        { duration: '2m', target: rps },        // recovery baseline
      ],
    },
  },
};

export default function () {
  const res = http.get(target);
  check(res, { 'status is 2xx/3xx': (r) => r.status >= 200 && r.status < 400 });
}
