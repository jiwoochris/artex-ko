# ARTEX GLM Coding Plan 연결

`scripts/glm-gateway.mjs`는 호스트의 Z.AI 키를 읽어 고정 Coding Plan Chat Completions 주소로 전달한다. Pi OAuth는 필요하지 않다. 구독 자격은 조회하지 않으며 공급자 정책과 사용 한도가 적용된다.

## 실행

Node.js 22 이상을 사용한다. 저장소 밖에 권한 0600의 JSON 설정 파일을 만든다.

```json
{
  "key": "REPLACE_WITH_RANDOM_GATEWAY_KEY_AT_LEAST_32_CHARACTERS",
  "credentialsFile": "/absolute/path/to/local-provider-secrets"
}
```

`key`는 API 키와 다른 난수 게이트웨이 키다. 기존 비밀 파일은 아래 키 이름을 사용하는 `KEY=value` 형식이다(따옴표 없이).

```text
BM_AGENT_LLM_BASE_URL=https://api.z.ai/api/coding/paas/v4
BM_AGENT_LLM_API_KEY=REPLACE_WITH_YOUR_LOCAL_KEY
```

```sh
node scripts/glm-gateway.mjs /absolute/path/to/gateway.json
```

호스트의 `127.0.0.1:18789`에만 바인드한다. 격리 환경에서는 기존 승인된 SSH 루프백 전달을 별도로 구성한다. 이 스크립트는 터널·방화벽·서비스 자동 시작을 설치하지 않는다. 실제 공급자 키를 게스트에 복사하지 않는다.

ARTEX에 다음 프로필을 추가하고 작업에서 명시적으로 선택한다.

- 형식: `openai`, 모델: `glm-5.3`
- Base URL: `http://127.0.0.1:18789/v1`
- API key: 위 별도 게이트웨이 키
- Streaming: 활성화, Max tokens: 16384

## 전송 경계

Upstream은 `https://api.z.ai/api/coding/paas/v4/chat/completions`로 고정한다. 텍스트와 function 호출·결과만 전달하며 호스트에서 도구를 실행하지 않는다. 프롬프트와 도구 결과는 Z.AI로 전송되므로 허가된 데이터만 사용한다.

다른 모델·임의 목적지·redirect·외부 이미지·공급자 hosted tool·Origin이 있는 브라우저 요청은 거부한다. 동시 2개, 분당 30회, 입력 2 MiB, 출력 16384 token, upstream 120초 제한이다. 공급자 오류 본문·프롬프트·자격증명을 로그에 기록하지 않는다. 인증 키를 변경한 경우 게이트웨이를 다시 시작한다.

## 검증

`node --test scripts/glm-gateway.test.mjs`

테스트는 로컬 가짜 upstream으로 함수 호출 전달, 출력 제한, 인증·요청 경계, 공급자 오류 비노출을 검증한다. 실제 공급자 연결이나 구독 가능 여부를 보장하는 테스트는 아니다.
