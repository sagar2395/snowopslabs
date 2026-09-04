// Errors profile — drives go-api into and out of simulated readiness failure so
// you can watch error rate, readiness, and alerts react under load. It toggles
// /toggle-failure periodically while a steady stream hits /ready and /.
// TRAFFIC_TARGET is treated as a BASE origin.
import http from 'k6/http';
import { check } from 'k6';

const base = (__ENV.TRAFFIC_TARGET || 'http://go-api.go-api.svc.cluster.local:8080/')
  .replace(/(https?:\/\/[^/]+).*/, '$1');
const rps = Number(__ENV.TRAFFIC_RPS || 10);
const duration = __ENV.TRAFFIC_DURATION || '10m';

export const options = {
  scenarios: {
    // Steady request stream that will see 503s while failure is toggled on.
    load: {
      executor: 'constant-arrival-rate',
      rate: rps,
      timeUnit: '1s',
      duration: duration,
      preAllocatedVUs: Math.max(10, rps * 2),
      maxVUs: Math.max(20, rps * 4),
      exec: 'hit',
    },
    // A single low-rate toggler that flips failure on/off every ~30s.
    toggler: {
      executor: 'constant-arrival-rate',
      rate: 1,
      timeUnit: '30s',
      duration: duration,
      preAllocatedVUs: 1,
      maxVUs: 1,
      exec: 'toggle',
    },
  },
  thresholds: {
    // Expect SOME failures here — this profile is about observing them, so the
    // threshold is loose and never aborts.
    http_req_failed: [{ threshold: 'rate<0.9', abortOnFail: false }],
  },
};

export function hit() {
  const res = http.get(`${base}/ready`);
  check(res, { 'ready or 503': (r) => r.status === 200 || r.status === 503 });
}

export function toggle() {
  http.post(`${base}/toggle-failure`);
}
