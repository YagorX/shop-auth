import grpc from 'k6/net/grpc';
import { check } from 'k6';

import { newClient, connect, uniq, register, login, ladder, startRate, APP_ID, CALL, SUMMARY } from './lib/auth.js';

// Refresh — единственная операция с транзакцией: INSERT новой сессии плюс UPDATE старой.
// Тест на путь записи.
const client = newClient();

export const options = {
  summaryTrendStats: SUMMARY,
  scenarios: {
    refresh: {
      executor: 'ramping-arrival-rate',
      startRate: startRate(5),
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: 400,
      // Первая ступень — разогрев: на первой итерации каждый VU делает свой Register и Login,
      // а это bcrypt. Цифры читать с второй ступени и дальше.
      stages: ladder([
        { target: 10, duration: '30s' },
        { target: 50, duration: '2m' },
        { target: 150, duration: '2m' },
        { target: 400, duration: '2m' },
      ]),
    },
  },
  thresholds: {
    grpc_req_duration: ['p(99)<200'],
    checks: ['rate>0.99'],
  },
};

// Состояние на уровне модуля — у каждого VU своё: refresh-токен ротируется,
// и каждая итерация обязана предъявлять токен, полученный в предыдущей.
// Один общий токен из setup дал бы поток Unauthenticated со второго запроса.
let session = null;

export default function () {
  if (session === null) {
    connect(client);

    const username = uniq('refresh');
    register(client, username);

    // device_id должен совпадать у Login и всех последующих Refresh:
    // сессия ищется по паре (хеш токена, device_id, app_id).
    const deviceId = `k6-refresh-device-${__VU}`;
    const tokens = login(client, username, deviceId);

    session = { refresh: tokens.refresh, deviceId };
  }

  const res = client.invoke(
    'proto.auth.v1.AuthService/Refresh',
    { refresh_token: session.refresh, app_id: APP_ID, device_id: session.deviceId },
    CALL,
  );

  const ok = check(res, { 'status OK': (r) => r.status === grpc.StatusOK });

  if (ok) {
    session.refresh = res.message.refreshToken;
  } else {
    // Цепочка порвалась (например, сработал дедлайн, а ротация прошла).
    // Без сброса VU до конца прогона будет бить в недействительный токен и портить статистику.
    session = null;
  }
}
