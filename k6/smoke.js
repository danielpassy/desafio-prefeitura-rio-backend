import http from 'k6/http';
import { check, fail } from 'k6';
import { hmac } from 'k6/crypto';

export const options = {
  vus: 1,
  iterations: 1,
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8081';
const IDP_URL = __ENV.IDP_URL || 'http://localhost:15423';
const WEBHOOK_SECRET = __ENV.WEBHOOK_SECRET || 'dev-webhook-secret';

function getToken(cpf) {
  const claims = JSON.stringify({ preferred_username: cpf });
  const res = http.post(
    `${IDP_URL}/default/token`,
    `grant_type=client_credentials&client_id=${cpf}&client_secret=secret&claims=${encodeURIComponent(claims)}`,
    { headers: { 'Content-Type': 'application/x-www-form-urlencoded' } },
  );
  if (res.status !== 200) {
    fail(`failed to get token for ${cpf}: ${res.status} ${res.body}`);
  }
  return JSON.parse(res.body).access_token;
}

function signedWebhookRequest(cpf, ticketId) {
  const body = JSON.stringify({
    ticket_id: ticketId,
    type: 'status_change',
    cpf: cpf,
    previous_status: 'open',
    new_status: 'in_progress',
    title: `Ticket ${ticketId} updated`,
    timestamp: new Date().toISOString(),
  });
  const sig = hmac('sha256', WEBHOOK_SECRET, body, 'hex');
  return {
    body,
    headers: {
      'Content-Type': 'application/json',
      'X-Signature-256': `sha256=${sig}`,
    },
  };
}

export default function () {
  const cpf = '00000000001';
  const token = getToken(cpf);
  const authHeaders = { Authorization: `Bearer ${token}` };

  const req = signedWebhookRequest(cpf, 'SMOKE-001');
  const wRes = http.post(`${BASE_URL}/webhook`, req.body, { headers: req.headers });
  check(wRes, { 'webhook: 201': (r) => r.status === 201 });

  const nRes = http.get(`${BASE_URL}/notifications`, { headers: authHeaders });
  check(nRes, {
    'notifications: 200': (r) => r.status === 200,
    'notifications: total >= 1': (r) => JSON.parse(r.body).total >= 1,
  });

  const ucRes = http.get(`${BASE_URL}/notifications/unread-count`, { headers: authHeaders });
  check(ucRes, {
    'unread-count: 200': (r) => r.status === 200,
    'unread-count: >= 1': (r) => JSON.parse(r.body).unread_count >= 1,
  });

  const items = JSON.parse(nRes.body).items;
  if (items && items.length > 0) {
    const rRes = http.patch(`${BASE_URL}/notifications/${items[0].id}/read`, null, { headers: authHeaders });
    check(rRes, { 'mark-read: 200': (r) => r.status === 200 });
  }
}
