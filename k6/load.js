import http from 'k6/http';
import { check, sleep } from 'k6';
import { hmac } from 'k6/crypto';

export const options = {
  scenarios: {
    ingest: {
      executor: 'constant-vus',
      vus: 10,
      duration: '30s',
      exec: 'ingestWebhook',
    },
    reads: {
      executor: 'constant-vus',
      vus: 20,
      duration: '30s',
      // start after some data is already ingested
      startTime: '5s',
      exec: 'readNotifications',
    },
  },
  thresholds: {
    'http_req_duration{scenario:ingest}': ['p(95)<300'],
    'http_req_duration{scenario:reads}': ['p(95)<500'],
    http_req_failed: ['rate<0.01'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8081';
const IDP_URL = __ENV.IDP_URL || 'http://localhost:15423';
const WEBHOOK_SECRET = __ENV.WEBHOOK_SECRET || 'dev-webhook-secret';
const NUM_CITIZENS = 20;

function cpfForIndex(i) {
  return String(i).padStart(11, '0');
}

export function setup() {
  const tokens = {};
  for (let i = 1; i <= NUM_CITIZENS; i++) {
    const cpf = cpfForIndex(i);
    const claims = JSON.stringify({ preferred_username: cpf });
    const res = http.post(
      `${IDP_URL}/default/token`,
      `grant_type=client_credentials&client_id=${cpf}&client_secret=secret&claims=${encodeURIComponent(claims)}`,
      { headers: { 'Content-Type': 'application/x-www-form-urlencoded' } },
    );
    if (res.status === 200) {
      tokens[cpf] = JSON.parse(res.body).access_token;
    }
  }
  return { tokens };
}

export function ingestWebhook() {
  const citizenIdx = ((__VU - 1) % NUM_CITIZENS) + 1;
  const cpf = cpfForIndex(citizenIdx);
  const ticketId = `CH-${__VU}-${__ITER}`;

  const body = JSON.stringify({
    ticket_id: ticketId,
    type: 'status_change',
    cpf: cpf,
    previous_status: 'open',
    new_status: 'in_progress',
    title: `Ticket ${ticketId}`,
    timestamp: new Date().toISOString(),
  });
  const sig = hmac('sha256', WEBHOOK_SECRET, body, 'hex');

  const res = http.post(`${BASE_URL}/webhook`, body, {
    headers: {
      'Content-Type': 'application/json',
      'X-Signature-256': `sha256=${sig}`,
    },
  });
  check(res, { 'webhook 200': (r) => r.status === 200 });
  sleep(0.1);
}

export function readNotifications(data) {
  const citizenIdx = ((__VU - 1) % NUM_CITIZENS) + 1;
  const cpf = cpfForIndex(citizenIdx);
  const token = data.tokens[cpf];
  if (!token) return;

  const headers = { Authorization: `Bearer ${token}` };

  const nRes = http.get(`${BASE_URL}/notifications?limit=20`, { headers });
  check(nRes, { 'list 200': (r) => r.status === 200 });

  const ucRes = http.get(`${BASE_URL}/notifications/unread-count`, { headers });
  check(ucRes, { 'unread-count 200': (r) => r.status === 200 });

  if (nRes.status === 200) {
    const unread = (JSON.parse(nRes.body).items || []).find((n) => !n.read);
    if (unread) {
      const rRes = http.patch(`${BASE_URL}/notifications/${unread.id}/read`, null, { headers });
      check(rRes, { 'mark-read 200': (r) => r.status === 200 });
    }
  }

  sleep(0.5);
}
