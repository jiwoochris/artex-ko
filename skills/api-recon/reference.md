# api-recon — 참고 매뉴얼

Grep 레시피, `config.json` 템플릿과 문제 해결. 모든 grep 은 `js/` 디렉터리를 대상으로 실행합니다. bundle 이 한 줄일 때는 먼저 `js-beautify` 또는 `sed 's/}/}\n/g'` 를 쓸 수 있고, 보통은 컨텍스트 창을 띄우는 raw grep 이면 충분합니다.

## 스크립트 설명

`scripts/` 안의 모든 파일은 **참고 템플릿**이며, 실행 전에 반드시 대상 사이트에 맞게 조정해야 합니다. 전형적인 수정 지점:

| 스크립트 | 자주 조정하는 항목 |
|---|---|
| `harvest_static.py` | endpoint 정규식, webpack/Vite manifest 파싱, 마이크로 프런트엔드 publicPath, 재시도/동시성 |
| `runtime_harvest.js` | neutralize 필드명과 성공값, stub 매칭 규칙과 body 구조, routes 출처, WS 녹화, `waitUntil`/`routeTimeout`/`proxy` |
| `preload.js` | `loginPathRe`, L1 stubs, `neutralize.fields`, `apiPattern`, L3 활성화 여부, `recordDetail`, `observe.*`, `neutralizeVueRouter` |
| `spider_mpa.py` | `--exclude` 파괴적 링크, cookie, depth/max, 동일 도메인 필터 |
| `extract_route_map.py` | `routeMap` / `routeLink` 정규식, KEY 명명 패턴 |
| `build_perm_tree.py` | `userRouteAuth` 파싱, `ROOTS`/`PREFIX_PARENT` 계층 휴리스틱, stub 바깥 계층 필드명 |
| `config.json` | 위 모든 사이트 전용 파라미터의 단일 진입점 |

조정한 파일은 작업 작업 디렉터리(예: `recon/`)에 두고, 보고서에 참고 스크립트 대비 구체적 변경점을 적는 것을 권장합니다.

---

## A. 리버싱의 세 관문

### A1. 렌더 관문 — 「로그인 여부를 어떻게 판단하는가?」

```bash
grep -rhoaE '.{0,40}(isLogin|isAuthenticated|loggedIn|hasLogin|requireAuth)\b.{0,80}' js | head
grep -rhoaE 'function (getUser|getToken|getAuth)[0-9]?\([^)]*\)\{.{0,200}' js | head
grep -rhoaE '(localStorage|sessionStorage)\.getItem\("[^"]+"\)' js | sort -u
grep -rhoaE '(Cookies?|cookie)\.(get|load)\("[^"]+"\)' js | sort -u
grep -rhoaE '\batob\(|JSON\.parse\(|jwt|decode' js | head
```

`isLogin = f(getUser())` → `getUser = decode(storage.read(KEY))` 체인을 찾아 **저장 키**, **컨테이너**(Cookie vs localStorage), **인코딩**을 확정:

| 인코딩 | config 위조 방식 |
|---|---|
| 평문 문자열 / `"1"` / token | `"value": "anything-truthy"` |
| `JSON.parse(x)` | `"value": "json:{\"id\":1,\"username\":\"admin\"}"` |
| `JSON.parse(atob(x))` | `"value": "b64json:{\"id\":1,\"username\":\"admin\"}"` |
| JWT | 서명 없는/`alg:none` JWT, 또는 bundle 내 키로 서명 |
| 암호화(SM2/AES/RSA) | 하드코딩된 키 찾기; 렌더 관문은 디코딩 가능한 blob 만 필요하면 forge, 아니면 정적 폴백 |

→ `cookies` / `localStorage` 에 기록.

### A2. 인터셉터 관문 — 「무엇이 /login 으로 튀게 하는가?」

```bash
grep -rhoaE '.{0,60}(interceptors\.response|axios|request\.use).{0,120}' js | head
grep -rhoaE '.{0,40}(response_code|errcode|errno|\bcode\b|\bret\b|\bstatus\b)\s*[=!]==?\s*[\-0-9]{1,4}.{0,60}' js | head -20
grep -rhoaE '.{0,40}(未登录|请重新登录|登录已过期|登录失效|授权|로그인|로그인이 필요|세션 만료|권한|인증|unauthorized|token.{0,10}invalid).{0,40}' js | head
grep -rhoaE '.{0,30}(location\.href|router\.(push|replace)|navigate)\([^)]*login[^)]*\)' js | head
```

확정 항목: **필드명**, **성공값**(보통 `0` 또는 `200`), **리다이렉트를 유발하는 실패값**. junk 세션으로 검증:

```bash
curl -sk -X POST -H 'Cookie: <fakekey>=junk' https://target/api/<protected> -d '{}' -H 'Content-Type: application/json'
```

→ `neutralize.fields` + `neutralize.success` 에 기록.

### A3. 콘텐츠 관문 — 「메뉴/권한은 어디서 오는가?」

```bash
grep -rhoaE '"/api[^"]*(permission|perm|role|menu|acl|resource|nav)[^"]*"' js | sort -u
grep -rhoaE '.{0,30}(menus|permissions|menuList|routeList|authList|role_permissions)\b.{0,120}' js | head
grep -rhoaE 'userRouteAuth|getResultTree|routeMap|routeLink|hasPermission|checkAuth' js | head
grep -rhoaE '([A-Z_][A-Z0-9_]*):\{name:"[^"]*",link:"/[^"]+"\}' js | head
```

**두 계층 데이터**(흔한 기업용 백오피스):

| API | 전형적 payload | 소비처 |
|---|---|---|
| `.../role_permissions` | `{ permissions: string[], role_type }` | 라우트 가드, 버튼 단위 ACL |
| `.../permissions/all` | `tree[{ code, position, children }]` | 사이드바 메뉴 렌더 |
| bundle 내 `userRouteAuth` | `{ CODE: { url, name? } }` | code → 프런트엔드 path |
| bundle 내 `routeMap` | `{ KEY: { name, link } }` | 별칭 해석(webpack `o.DASHBOARD`) |

소비처 코드를 읽어 확인: `getResultTree(tree, permissions)` 가 어떻게 필터하는지, `v-if` / `hasAuth(code)` 가 어떤 필드를 검사하는지.

**수동 forge**(소규모 사이트): permissive payload 를 만들어 → `stubs`.

**완전한 권한 트리 복원**(대규모 사이트, 사이드바/하위 모듈이 여전히 빈 경우): **I 절** 참고.

---

## B. config.json 템플릿

```json
{
  "baseUrl": "https://target/",
  "runtimeMode": "both",
  "chromium": "/usr/bin/chromium",

  "cookies": [
    { "name": "auth", "value": "b64json:{\"id\":1,\"username\":\"admin\",\"role\":\"admin\",\"func\":{},\"permissions\":[\"*\"]}" }
  ],
  "localStorage": { "token": "faketoken", "isLogin": "1" },

  "neutralize": {
    "fields": ["response_code", "code", "errno", "ret", "status"],
    "success": 0,
    "flags": { "success": true, "message": "ok" }
  },
  "forward": true,
  "loginUrlPattern": "/login",
  "apiPattern": "/api/|/rest/|/graphql",

  "mockTier": "L1+L2",
  "recordDetail": true,
  "observe": {
    "storageReads": false,
    "cookieReads": false,
    "xhrHeaders": true
  },
  "neutralizeVueRouter": true,
  "stubs": [
    {
      "match": "permissions/all|/menu|role_permissions",
      "body": {
        "response_code": 0, "code": 0,
        "data": {
          "permissions": ["*"],
          "menus": [
            { "name": "dashboard", "path": "/dashboard", "show": true, "children": [] },
            { "name": "alert", "path": "/alert", "show": true, "children": [] }
          ]
        }
      }
    }
  ],

  "explore": {
    "clickTabs": true,
    "clickTables": true,
    "pushStateFallback": true,
    "maxMenuItems": 50
  },

  "routes": ["/dashboard", "/alert", "/asset", "/device", "/report", "/config", "/system"],
  "waitMs": 1500, "perRouteMs": 900, "headless": true,
  "waitUntil": "domcontentloaded",
  "routeTimeout": 12000,
  "proxy": "",

  "captureResponses": true, "recordWs": true, "respMax": 600
}
```

필드 설명:
- `runtimeMode`: `depth`(Puppeteer), `coverage`(browser MCP), `both`
- `cookies[].value` 접두: `b64json:` → base64(JSON); `json:` → 원시 JSON; 접두 없음 → 리터럴
- `forward: true` 실제 요청을 전달하며 코드 필드를 재작성; `false` 완전 오프라인 stub
- `mockTier`: coverage 모드 preload 활성화 계층, 예 `L1+L2`, `L1+L2+L3`
- `routes` 는 `routes.txt` 에서; 메뉴 forge 후 harness 가 `<a href>` 를 자동 추가
- `captureResponses` / `recordWs` 는 depth 모드에서만 유효
- `waitUntil`: 대형 SPA 는 `domcontentloaded` 사용, `networkidle2` 로 멈추는 것을 피함
- `routeTimeout`: 단일 라우트 `page.goto` 타임아웃(밀리초)
- `proxy`: Puppeteer `--proxy-server`; `HTTP_PROXY` / `HTTPS_PROXY` 로도 설정 가능

### B1. 이중 stub 템플릿(role_permissions + permissions/all)

```json
"stubs": [
  {
    "match": "role_permissions",
    "body": {
      "response_code": 0,
      "data": {
        "permissions": ["MONITOR", "MONITOR_ALERT", "THREAT", "ASSETS_RISK"],
        "role_type": "SUPER_ADMIN"
      }
    }
  },
  {
    "match": "permissions/all",
    "body": {
      "response_code": 0,
      "data": [
        {
          "code": "MONITOR",
          "position": 1,
          "children": [
            { "code": "MONITOR_ALERT", "position": 1, "children": [] }
          ]
        }
      ]
    }
  }
]
```

바깥 계층 필드명(`response_code` / `code` / `data`)은 A2 인터셉터 관문과 일치해야 합니다; `permissions` 는 tree 의 모든 leaf code 를 포함해야 합니다.

---

## C. coverage 모드: preload 설정

`scripts/preload.js` 상단의 `CONFIG` 객체를 편집하거나, CDP 주입 전에 교체:

```javascript
const CONFIG = {
  loginPathRe: /\/(login|signin)(\/|$|\?)/i,
  mockTier: 'L1+L2',
  forward: true,
  recordDetail: true,
  extractUrlsFromResponse: true,
  neutralizeVueRouter: true,
  observe: { storageReads: false, cookieReads: false, xhrHeaders: true },
  neutralize: { fields: ['response_code', 'code'], success: 0 },
  stubs: [ /* config.json 의 stubs 와 동일 */ ],
  apiPattern: /\/(api|apis|v\d+|dev|internal|graphql)\//i,
};
```

검증: `window.__API_RECON_PRELOAD__ === true` 이고 pathname 이 안정적인지.

녹화 결과 내보내기:

```javascript
JSON.stringify({
  apis: [...window.__API_RECON_LOG__],
  detail: window.__API_RECON_DETAIL__,
  routes: [...(window.__API_RECON_ROUTES__ || [])],
  observe: window.__API_RECON_OBSERVE__,
}, null, 2)
```

---

## D. preload / runtime Hook 능력

preload(coverage)와 runtime_harvest(depth)에 내장된 브라우저 Hook 능력과 커버리지 범위:

| Hook 능력 | API 발견에 대한 가치 | 커버리지 |
|---|---|---|
| Hook fetch / XHR.open | 요청 URL/메서드 녹화 | ✅ `recordDetail` + `__API_RECON_LOG__` |
| Hook XHR.setRequestHeader | Authorization 등 헤더 발견 | ✅ `observe.xhrHeaders` |
| Hook localStorage/cookie 읽기 | 세션 키명 확인 | ⚠️ 선택 `observe.storageReads/cookieReads` |
| Vue 라우트 획득 | frontendRoutes 보완 | ✅ `__API_RECON_ROUTES__`(로드된 라우트) |
| Vue 라우트 가드 중화 / 로그인 리다이렉트 차단 | 모듈을 펼쳐 API 유발 | ✅ `neutralizeVueRouter` + 네이티브 리다이렉트 중화 |
| React 라우트 획득 | 라우트 보완 | ⚠️ 정적 + 클릭; 전용 Hook 없음 |
| 페이지 리다이렉트 차단(로그인 path) | 페이지에 머물러 분석 | ⚠️ 로그인 path 만 차단, 업무 내비게이션은 막지 않음 |
| Hook 암호화 라이브러리(CryptoJS/SM 등) | 암호화 파라미터 → 평문 API body | ❌ 암호화 함수 인자를 수동 Hook 해야 함; 결론은 config 에 기록 |
| 안티 디버깅 bypass | 안 하면 runtime 에서 API 를 못 녹화 | ❌ 수동 처리 필요; 정적은 여전히 사용 가능 |

---

## E. Endpoint 추출 정규식(정적 결과가 너무 적을 때)

`harvest_static.py` 의 `extract_endpoints` 를 느슨하게 하거나, 수동으로:

```bash
grep -rhoaE '"/[a-z][A-Za-z0-9_/\-]{3,}"' js | sort -u
grep -rhoaE '/api/[a-zA-Z0-9_./-]+' js | sort -u
```

---

## F. 문제 해결

| 현상 | 원인 → 처리 |
|---|---|
| 정적 API 가 매우 적음 | endpoint 방언 불일치 → 정규식 완화(D 절) |
| chunk 수 ≪ manifest | CSS 전용이거나 미배포 chunk; 404 는 이미 재시도됨 |
| runtime 이 여전히 로그인 페이지 표시 | 렌더 관문 오류 → A1 재점검: 키명, 컨테이너, 인코딩, domain |
| 셸에 진입했으나 모듈이 빈 화면 | 콘텐츠 관문 → 메뉴 forge(A3); `routes` path 가 틀렸을 수 있음 |
| 라우트마다 bootstrap/locale 만 | 권한 코드 불완전 → I 절 권한 트리 복원; `role_permissions` + `permissions/all` 이중 stub 확인 |
| 사이드바에 항목은 있으나 하위 페이지 빈 화면 | tree 에 중간 노드가 없거나 code 가 `userRouteAuth` 와 불일치 |
| 모든 API 가 로그인으로 튐 | 인터셉터 관문 → `neutralize` 확인; 중첩 필드는 walk 로직 확장 필요 |
| WS 프레임이 0 | 사용자 상호작용 후에야 subscribe; `perRouteMs` 를 늘림 |
| 응답 본문이 빔 | `forward: true` 일 때만 실제 응답이 있음 |
| Chromium 없음 | chromium 설치 또는 `config.chromium` / `CHROMIUM` 설정 |
| Mock 이 많아도 여전히 로그인으로 돌아감 | Hook 이 너무 늦거나 `location.href` setter 누락 → document-start + preload |
| 목록이 전부 빔 | L3 빈 배열은 정상; 계속 Tab/설정/상세 클릭 |
| Redux action 을 라우트로 오인 | get/set/change/clear/toggle/upload 가 든 내부 path 를 필터 |
| Vue 가 여전히 로그인으로 튐 | preload 가 document-start 아님 → 주입 시점 변경; 또는 `neutralizeVueRouter: false` 일 때 수동으로 가드 제거 |
| 응답에 URL 이 있으나 log 에 안 들어감 | `extractUrlsFromResponse` 켜기; 또는 `__API_RECON_DETAIL__` 에서 수동 추출 |
| Authorization 헤더 이름을 모름 | `observe.xhrHeaders` 켜기 또는 DevTools 로 요청 헤더 확인 |
| runtime 이 매우 느림 / 타임아웃 | `waitUntil: domcontentloaded` 로 변경; `routeTimeout` 낮춤; `networkidle2` 쓰지 말 것 |
| 프록시 연결 실패 | `proxy` / 환경 변수 확인; Puppeteer 와 curl 의 프록시 포트 일치 |

---

## G. hardened 대상

서버 측이 세션을 단계적으로 검증(forge 불가한 서명 cookie, 서버 렌더링이라 stub 불가한 메뉴)하면, runtime 이 셸 단계에서 멈춥니다. 예상 동작:

- **정적만으로 endpoint 열거는 충분** — 모듈 path 가 코드 안에 있음
- 승인된다면 **실제 세션**으로 같은 harness 실행: `forward: true`, neutralize 불필요, 실제 methods/params/responses 캡처

---

## H. 단일 작업 체크리스트

1. 승인 범위 확인
2. `scripts/harvest_static.py` **읽기** → 대상에 맞게 조정 → 실행 → `api_static.txt`, `routes.txt` 검토
3. **Phase 1b**: path 앵커 확장 창 + 바인딩 계층 → `param_candidates.json`(J 절)
4. 리버싱 A1/A2/A3 → 사이트 전용 `config.json` 작성
5. `runtime_harvest.js` / `preload.js` 를 **읽고 조정**한 뒤 실행
6. `runtimeMode=depth`: `npm install` → 조정한 harvest 스크립트 실행
7. `runtimeMode=coverage/both`: document-start 로 조정한 preload 주입 → browser MCP 로 동적 열거 + **파라미터 유발 매트릭스**
8. 모듈이 렌더링 안 됨 → **I 절 권한 트리 복원** → stubs patch → 재실행
9. 파라미터 다중 샘플 diff + 오류 역추론 → `params_merged.json`
10. 병합 → `site_map.json` + `api_merged.txt`, 커버리지·공백·스크립트 변경점을 정직하게 표기

---

## I. 권한 트리 복원(Phase 4 심화)

단순한 `menus: [{ path, show: true }]` forge 가 안 먹히고 하위 모듈이 여전히 mount 안 될 때 사용.

### I1. auth 모듈 위치 찾기

```bash
grep -l 'userRouteAuth' js/*.js
grep -l 'routeMap\|routeLink' js/*.js
grep -rhoaE 'getResultTree|role_permissions|permissions/all' js | head
```

기록: **권한 API path**, **응답 필드명**, **소비 chunk 파일명**.

### I2. routeMap 추출

```bash
python3 scripts/extract_route_map.py recon/js recon/
# recon/route_map.json 산출
```

`[!] no routeMap pattern found` 이면: `extract_route_map.py` 의 정규식을 완화하거나 수동 grep:

```bash
grep -rhoaE '([A-Z_][A-Z0-9_]*):\{name:"[^"]*",link:"/[^"]+"\}' js | head -20
```

### I3. 권한 트리 구성 + stub

```bash
python3 scripts/build_perm_tree.py recon/js recon/ --config recon/config.json
```

스크립트 로직:
1. `userRouteAuth={MONITOR:{url:...},...}` 파싱(webpack 별칭 `He=o.DASHBOARD` 포함)
2. `route_map.json` 으로 alias → 실제 path 해석
3. code 접두로 parent 추론(`MONITOR_ALERT` → `MONITOR`)
4. `permissions_tree.json`, `permissions_all_stub.json`, `role_permissions_stub.json` 출력
5. `--config` 시 `config.json` 의 `stubs` 와 확장된 `routes` 를 자동 기록

**대상에 맞게 조정**(스크립트 상단):
- `DEFAULT_ROOTS`: 최상위 모듈 code 목록
- `DEFAULT_PREFIX_PARENT`: `PREFIX_` → parent 매핑
- `DEFAULT_EXTRA_PARENT`: 접두 관계가 아닌 orphan 노드

### I4. stub 일관성 검증

```bash
# permissions 수는 userRouteAuth 항목 수와 ≈ 해야 함
wc -l recon/perm_codes_all.txt
# routes 는 route_map 의 모든 link 를 포함해야 함
python3 -c "import json; m=json.load(open('recon/route_map.json')); r=set(json.load(open('recon/config.json'))['routes']); print('missing', [v['link'] for v in m.values() if v['link'] not in r])"
```

### I5. runtime 재실행 및 비교

```bash
node recon/runtime_harvest.js recon/config.json
# forge 전후 runtime_api.json 건수 비교; /attack, /asset 등에 모듈 API 가 나타나는지 확인
```

| forge 전 | forge 후(성공) |
|---|---|
| 라우트마다 동일한 3–5 건 bootstrap | 서로 다른 라우트가 서로 다른 module API 유발 |
| `/api/locale/language` 뿐 | `/api/web/...` 모듈 endpoint 등장 |
| `routes.txt` 한 자릿수 라우트 | route_map 에서 온 `routes` 80–110+ |

### I6. 그래도 실패할 때

- **coverage 모드**: 사이드바 + Tab 클릭, 권한 gating 이 상호작용 후에야 요청될 수 있음
- **stub 필드**: 실제 API(curl + 실제 session)와 stub 의 nesting 비교
- **추가 가드**: `hasPermission|checkRole|func.` 등 버튼 단위 검사를 grep 해 `role_permissions.permissions` 확장
- **정적 폴백**: 모듈 API path 는 여전히 `api_static.txt` 에 있음, runtime 은 METHOD/body 만 보완; 파라미터는 `param_candidates.json` + 이미 녹화한 샘플 유지

---

## J. 파라미터 리버싱(Phase 1b / 5b / 5c)

**범용 스크립트가 아닌 방법론.** path 는 정규식으로 찾고, 파라미터는 앵커 확장 창 + UI 바인딩 체인 + 다중 샘플 diff + 오류 역추론으로 찾습니다.

### J1. 앵커 확장 창 — path 에서 조립 객체 찾기

```bash
# Phase 1 에서 알아낸 path 를 앵커로
grep -n '"/api/user/list"' js/*.js
grep -rhoaE '.{0,120}("/api[^"]+").{0,200}' js | head
grep -rhoaE '(params|data|body|payload)\s*:\s*\{' js | head
grep -rhoaE '(get|post|put|delete|patch)\([^,]+,\s*\{' js | head
```

### J2. 래핑 계층과 전송 형태

```bash
# axios / 통합 request
grep -rhoaE '(axios|request)\.(get|post|put|delete|patch)\(' js | head
grep -rhoaE 'interceptors\.(request|response)' js | head

# GraphQL
grep -rhoaE '(query|mutation)\s+\w+|gql`|graphql\(' js | head
grep -rhoaE '\$[a-zA-Z_]+\s*:\s*(Int|String|Boolean|\[)' js | head

# FormData / multipart
grep -rhoaE 'FormData|\.append\(' js | head

# 경로 파라미터
grep -rhoaE 'path:\s*"/[^"]*:[^"]+"' js | head
grep -rhoaE 'useParams|route\.params|\$route\.params' js | head
```

### J3. 검증 관문 — 필수 / 형식 / 열거

```bash
grep -rhoaE '(required|message|pattern|enum|validator)\s*:' js | head
grep -rhoaE 'yup\.|zod\.|async-validator|Form\.Item|a-form-item|el-form-item' js | head
grep -rhoaE 'rules\s*:\s*\[|name:\s*["\'][a-zA-Z_]+["\']' js | head
grep -rhoaE 'label.*value|options\s*:\s*\[' js | head
```

### J4. 바인딩 계층 — 폼 → API

```bash
grep -rhoaE 'onFinish|handleSubmit|getFieldsValue|validateFields' js | head
grep -rhoaE '(pick|omit|transform|dayjs|moment)\(' js | head
```

runtime 보완: DevTools → Network → 요청 → **개시자**(call stack)에서 `fetch`/`send` 로부터 위로 조립 함수를 추적.

### J5. 암호화 파라미터

```bash
grep -rhoaE 'encrypt|decrypt|sign|CryptoJS|sm2|sm3|sm4|RSA|AES' js | head
```

**암호문 위에서 필드를 추측하지 말 것** — 암호화 함수의 **인자**를 Hook 해, 암호화 전 plaintext payload 를 녹화; 결론은 `config.json` / `param_candidates.json` 에 기록.

### J6. 파라미터 유발 매트릭스(Phase 3 필수)

각 모듈에 대해 작업별로 한 번씩 녹화하고, 요청 body/query 를 diff:

| 작업 | 관심 포인트 |
|---|---|
| 목록 첫 화면 | 페이지네이션 기본값 |
| 검색 | keyword, filters |
| 고급 필터 | optional 필드 |
| 생성/편집 | 완전한 entity |
| 배치/내보내기 | `ids[]`, `exportType` |
| 정렬/페이지 넘김 | `sortField`, `order` |

산출 `param_samples.json`: `[{ "path", "method", "action": "search", "body", "query", "headers" }]`

### J7. 신뢰도 규칙

| 신뢰도 | 조건 |
|---|---|
| **높음** | 정적 callsite + runtime ≥2 샘플 일치 |
| **중간** | 정적만, 또는 runtime 1회만 |
| **낮음** | 응답/오류 역추론, 2차 검증 안 됨 |
| **유발 대기** | 정적으로 알려진 필드, UI/권한이 아직 실행 안 됨 |

### J8. 시나리오 빠른 설정

| 시나리오 | 순서 |
|---|---|
| REST 목록 페이지 | J1 조립 객체 → J6 4회 diff → J3 rules |
| 생성/편집 폼 | J3 Form name → J4 submit 체인 → runtime 제출 + 일부러 비워 400 확인 |
| GraphQL | J2 variables 선언 → runtime 각 operation 의 variables 녹화 |
| 암호화 body | J5 인자 Hook → 암호화 전 필드가 곧 실제 params |

### J9. api-recon 단계 매핑

| api-recon | 파라미터 recon |
|---|---|
| Phase 1 정적 | J1 앵커 확장 창 |
| Phase 2 A2 인터셉터 | 전역 주입 필드(tenantId, sign) |
| Phase 3 runtime | J6 유발 매트릭스 + `param_samples.json` |
| Phase 4 권한 트리 | 모듈별 폼이 다름 → 권한이 충분해야 전체 필드 유발 |
| Phase 5 병합 | `params_merged.json` + 신뢰도; 단일 샘플로 필수 여부를 단정 말 것 |

### J10. 문제 해결

| 현상 | 처리 |
|---|---|
| 정적에 필드명이 있으나 runtime 에 한 번도 안 나타남 | 「유발 대기」로 표기; 권한 트리 보완 / 고급 필터 클릭 / 연동 select 의 각 option |
| 같은 path 에 다른 body 형태 | 정상 — `action` 별로 나눠 기록, 억지로 schema 병합 말 것 |
| stub 응답은 가짜지만 params 를 보고 싶음 | **outbound 요청**의 body/headers 를 볼 것, stub 응답에서 역추론 말 것 |
| 400 이 nested field 를 보고함 | 바깥 래퍼 `data`/`bizData`/`variables` 에 주의 |
| GraphQL 에서 operation 이름만 보임 | `variables` JSON 을 펼치기; 정적에서 `$var: Type` 찾기 |

---
