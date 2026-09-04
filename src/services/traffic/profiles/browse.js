// Browse profile — a realistic read-mix across several endpoints, not a single
// hammered path. Shows how real traffic spreads over routes. TRAFFIC_TARGET is
// treated as a BASE origin (scheme://host:port); the paths below are appended.
import http from 'k6/http';
import { check, group } from 'k6';

// Derive the origin from TRAFFIC_TARGET, dropping any path it carries.
const base = (__ENV.TRAFFIC_TARGET || 'http://go-api.go-api.svc.cluster.local:8080/')
  .replace(/(https?:\/\/[^/]+).*/, '$1');
const rps = Number(__ENV.TRAFFIC_RPS || 10);
const duration = __ENV.TRAFFIC_DURATION || '10m';

// Weighted endpoint mix: mostly the landing page, some version checks, a few
// health polls — the shape of everyday read traffic.
const endpoints = [
  { path: '/', weight: 70 },
  { path: '/version', weight: 20 },
  { path: '/health', weight: 10 },
];
const total = endpoints.reduce((s, e) => s + e.weight, 0);

function pick() {
  let n = Math.random() * total;
  for (const e of endpoints) {
    if ((n -= e.weight) <= 0) return e.path;
  }
  return '/';
}

export const options = {
  scenarios: {
    browse: {
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
  const path = pick();
  group(path, () => {
    const res = http.get(`${base}${path}`);
    check(res, { 'status is 2xx/3xx': (r) => r.status >= 200 && r.status < 400 });
  });
}
