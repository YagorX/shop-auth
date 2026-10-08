import grpc from 'k6/net/grpc';

export const ADDR = __ENV.AUTH_ADDR || '127.0.0.1:44044';
export const APP_ID = Number(__ENV.APP_ID || '1');
export const PASSWORD = __ENV.K6_PASSWORD || 'Test123!';

// Таймаут вызова больше серверного дедлайна (grpc.timeout = 5s в конфиге):
// хотим видеть DeadlineExceeded от сервиса, а не обрыв со стороны k6.
export const CALL = { timeout: '10s' };

// По умолчанию k6 показывает p(90)/p(95); нам нужен p(99) — на него выставлены пороги.
export const SUMMARY = ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'];

export function newClient() {
  // new grpc.Client() обязан создаваться на init-стадии, то есть на уровне модуля.
  // Соединение подключается отдельно: connect() работает только внутри setup или VU-кода.
  //
  // Если на сервере отключат reflection, заменить reflect на загрузку proto:
  //   client.load(['/contracts'], 'auth/v1/auth.proto');
  // и смонтировать контракты: -v "$PWD/../../shop-contracts/proto:/contracts"
  return new grpc.Client();
}

export function connect(client) {
  client.connect(ADDR, { plaintext: true, reflect: true });
}

// Префикс k6- обязателен: по нему чистятся тестовые пользователи между прогонами.
export function uniq(prefix) {
  return `k6-${prefix}-${__VU}-${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
}

export function register(client, username) {
  const res = client.invoke(
    'proto.auth.v1.AuthService/Register',
    { email: `${username}@k6.local`, password: PASSWORD, username },
    CALL,
  );

  if (res.status !== grpc.StatusOK) {
    throw new Error(`register ${username} failed: status=${res.status} ${JSON.stringify(res.error)}`);
  }

  return username;
}

export function login(client, emailOrName, deviceId) {
  const res = client.invoke(
    'proto.auth.v1.AuthService/Login',
    { email_or_name: emailOrName, password: PASSWORD, app_id: APP_ID, device_id: deviceId },
    CALL,
  );

  if (res.status !== grpc.StatusOK) {
    throw new Error(`login ${emailOrName} failed: status=${res.status} ${JSON.stringify(res.error)}`);
  }

  // В запросах поля в snake_case, как в proto, а в ответе k6 отдаёт их в camelCase.
  return { access: res.message.accessToken, refresh: res.message.refreshToken };
}

// Калибровка: -e CALIBRATE=1 заменяет ступени одной минутой на 1 rps.
// Нужна, чтобы отделить ошибки сценария от насыщения сервиса.
export function ladder(stages) {
  if (__ENV.CALIBRATE) {
    return [{ target: 1, duration: '1m' }];
  }

  return stages;
}

// startRate тоже приходится опускать: иначе ramping-arrival-rate начнёт со штатного
// значения и будет спускаться к 1, то есть калибровка пройдёт под реальной нагрузкой.
export function startRate(normal) {
  return __ENV.CALIBRATE ? 1 : normal;
}
