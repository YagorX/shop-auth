import grpc from 'k6/net/grpc';
import { check } from 'k6';
import { Rate, Trend } from 'k6/metrics';

import { newClient, connect, uniq, register, login, PASSWORD, APP_ID, CALL, SUMMARY } from './lib/auth.js';

// Ключевой прогон всего этапа: дорогой Login и дешёвый ValidateToken одновременно.
// Если p99 у validate вырастет вслед за нагрузкой на login — значит дорогой метод
// топит дешёвые, и это прямое обоснование bulkhead из этапа 3 плана.
//
// LOGIN_RATE ≈ 80% от колена login проверяет, что ниже потолка всё спокойно.
// LOGIN_RATE выше колена проверяет семафор на bcrypt: лишние логины должны получать
// быстрый ResourceExhausted, а validate — оставаться в миллисекундах.
const client = newClient();

// Отказ по перегрузке выше потолка — ожидаемый исход, а не ошибка: его считаем отдельно.
// Время меряется в сценарии, потому что ответ k6 не отдаёт тайминги вызова;
// миллисекундной точности для login (десятки мс) хватает.
const loginRejected = new Rate('login_rejected');
const loginOkDuration = new Trend('login_ok_duration', true);
const loginRejectedDuration = new Trend('login_rejected_duration', true);

const LOGIN_RATE = Number(__ENV.LOGIN_RATE || '100');
const VALIDATE_RATE = Number(__ENV.VALIDATE_RATE || '50');
const USERS = Number(__ENV.USERS || '50');
const DURATION = __ENV.DURATION || '3m';

export const options = {
  summaryTrendStats: SUMMARY,
  scenarios: {
    login: {
      executor: 'constant-arrival-rate',
      rate: LOGIN_RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 100,
      maxVUs: 500,
      exec: 'loginFlow',
    },
    validate: {
      executor: 'constant-arrival-rate',
      rate: VALIDATE_RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 50,
      maxVUs: 200,
      exec: 'validateFlow',
    },
  },
  thresholds: {
    // Пороги по сценариям раздельные: сравнение именно между ними и есть результат прогона.
    // Без семафора validate выше колена login уходил в секунды (p99 3.4 с).
    'grpc_req_duration{scenario:validate}': ['p(99)<50'],
    'checks{scenario:validate}': ['rate>0.99'],
    // Login отвечает либо успехом, либо честным отказом — никаких Internal и DeadlineExceeded.
    'checks{scenario:login}': ['rate>0.99'],
    'login_ok_duration': ['p(99)<300'],
    // Отказ обязан быть быстрым: max_wait (150 мс) плюс сеть, а не дедлайн клиента.
    'login_rejected_duration': ['p(99)<250'],
  },
};

export function setup() {
  connect(client);

  const users = [];
  for (let i = 0; i < USERS; i += 1) {
    users.push(register(client, uniq('mixed')));
  }

  const tokens = login(client, users[0], 'k6-mixed-device');

  client.close();

  return { users, token: tokens.access };
}

let connected = false;

function ensureConnected() {
  if (!connected) {
    connect(client);
    connected = true;
  }
}

export function loginFlow(data) {
  ensureConnected();

  const username = data.users[Math.floor(Math.random() * data.users.length)];

  const started = Date.now();
  const res = client.invoke(
    'proto.auth.v1.AuthService/Login',
    {
      email_or_name: username,
      password: PASSWORD,
      app_id: APP_ID,
      device_id: `k6-mixed-login-${__VU}`,
    },
    CALL,
  );
  const elapsed = Date.now() - started;

  const ok = res.status === grpc.StatusOK;
  const rejected = res.status === grpc.StatusResourceExhausted;

  loginRejected.add(rejected);
  if (ok) {
    loginOkDuration.add(elapsed);
  }
  if (rejected) {
    loginRejectedDuration.add(elapsed);
  }

  check(res, { 'OK or ResourceExhausted': () => ok || rejected });
}

export function validateFlow(data) {
  ensureConnected();

  const res = client.invoke(
    'proto.auth.v1.AuthService/ValidateToken',
    { token: data.token, app_id: APP_ID },
    CALL,
  );

  check(res, { 'status OK': (r) => r.status === grpc.StatusOK });
}
