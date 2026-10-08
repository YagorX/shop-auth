import grpc from 'k6/net/grpc';
import { check } from 'k6';

import { newClient, connect, uniq, register, login, ladder, startRate, APP_ID, CALL, SUMMARY } from './lib/auth.js';

// ValidateToken — самый частый метод в проде: разбор JWT плюс один SELECT по uuid.
// Именно его этап 2 плана убирает с горячего пути, поэтому цифра отсюда — точка отсчёта.
const client = newClient();

export const options = {
  summaryTrendStats: SUMMARY,
  scenarios: {
    validate: {
      executor: 'ramping-arrival-rate', // задаём rps, а не число VU
      startRate: startRate(10),
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: 600,
      stages: ladder([
        { target: 100, duration: '2m' },
        { target: 500, duration: '2m' },
        { target: 1500, duration: '2m' },
        { target: 4000, duration: '2m' },
      ]),
    },
  },
  thresholds: {
    grpc_req_duration: ['p(99)<200'],
    checks: ['rate>0.99'],
  },
};

export function setup() {
  connect(client);

  const username = uniq('validate');
  register(client, username);
  const tokens = login(client, username, 'k6-validate-device');

  client.close();

  return { token: tokens.access };
}

let connected = false;

export default function (data) {
  if (!connected) {
    // Одно соединение на VU, а не на запрос: повторный connect в том же VU завершится ошибкой.
    connect(client);
    connected = true;
  }

  const res = client.invoke(
    'proto.auth.v1.AuthService/ValidateToken',
    { token: data.token, app_id: APP_ID },
    CALL,
  );

  check(res, { 'status OK': (r) => r.status === grpc.StatusOK });
}
