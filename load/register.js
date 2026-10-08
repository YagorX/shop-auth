import grpc from 'k6/net/grpc';
import { check } from 'k6';

import { newClient, connect, uniq, ladder, startRate, PASSWORD, CALL, SUMMARY } from './lib/auth.js';

// Register — bcrypt на хеширование плюс INSERT. Самый дорогой метод из всех,
// и в проде самый редкий: по приоритету при перегрузке отбрасывается первым.
const client = newClient();

export const options = {
  summaryTrendStats: SUMMARY,
  scenarios: {
    register: {
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

let connected = false;

export default function () {
  if (!connected) {
    connect(client);
    connected = true;
  }

  const username = uniq('register');

  const res = client.invoke(
    'proto.auth.v1.AuthService/Register',
    { email: `${username}@k6.local`, password: PASSWORD, username },
    CALL,
  );

  // AlreadyExists здесь означал бы коллизию имён, то есть ошибку генератора, а не сервиса.
  check(res, {
    'status OK': (r) => r.status === grpc.StatusOK,
    'no duplicates': (r) => r.status !== grpc.StatusAlreadyExists,
  });
}
