import grpc from 'k6/net/grpc';
import { check } from 'k6';

import { newClient, connect, uniq, register, ladder, startRate, PASSWORD, APP_ID, CALL, SUMMARY } from './lib/auth.js';

// Login — проверка пароля через bcrypt, то есть тест на CPU.
// Порог p99 здесь выше остальных: ~70 мс на bcrypt это нормальная работа, а не деградация.
const client = newClient();

const USERS = Number(__ENV.USERS || '50');

export const options = {
  summaryTrendStats: SUMMARY,
  scenarios: {
    login: {
      executor: 'ramping-arrival-rate',
      startRate: startRate(5),
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: 400,
      stages: ladder([
        { target: 25, duration: '2m' },
        { target: 60, duration: '2m' },
        { target: 120, duration: '2m' },
        { target: 250, duration: '2m' },
      ]),
    },
  },
  thresholds: {
    grpc_req_duration: ['p(99)<300'],
    checks: ['rate>0.99'],
  },
};

export function setup() {
  connect(client);

  // Пул пользователей, а не один на всех: иначе все запросы идут в одну строку
  // и измеряется работа кэша Postgres, а не нормальный путь.
  const users = [];
  for (let i = 0; i < USERS; i += 1) {
    users.push(register(client, uniq('login')));
  }

  client.close();

  return { users };
}

let connected = false;

export default function (data) {
  if (!connected) {
    connect(client);
    connected = true;
  }

  const username = data.users[Math.floor(Math.random() * data.users.length)];

  const res = client.invoke(
    'proto.auth.v1.AuthService/Login',
    {
      email_or_name: username,
      password: PASSWORD,
      app_id: APP_ID,
      device_id: `k6-login-device-${__VU}`,
    },
    CALL,
  );

  check(res, { 'status OK': (r) => r.status === grpc.StatusOK });
}
