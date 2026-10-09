# ARTEX 한국어판 리버스엔지니어링 결과 보고서

- 대상 저장소: `https://github.com/quantum-decrypt-security/artex-ko`
- 분석 경로: `/home/user/artex-ko`
- 분석 일자: 2026-10-09
- 분석 방식: 소스 코드 정적 분석 (런타임 실행 미수행)
- 분석 관점: 방어·탐지 목적의 아키텍처 역공학 (권한 있는 환경 전제)

---

## 0. 요약 (Executive Summary)

- ARTEX 는 **LLM 멀티 에이전트가 정찰·침투·검증을 자율로 수행**하는 침투 테스트 플랫폼임
- 구조는 **Go 단일 백엔드 + Next.js 프런트엔드(바이너리 내장) + PostgreSQL** 로 구성됨
- 에이전트 기능의 하부는 외부 SDK `github.com/Autumn-27/norma` 가 제공함 (세션 루프·LLM 추상화·기본 도구)
- 핵심 설계는 **이중 그래프 블랙보드**(자산 그래프 + 탐색 그래프)와 **이벤트 구동 폐곡선 엔진**임
- 본 저장소는 원본(중국산 오픈소스) 프로젝트의 **한국어 현지화 판본**이며, 내부 추론 프롬프트는 원문 유지·사용자 노출 산출물만 한국어 강제함
- 특기할 점: 저장소가 **ARTEX 자신을 탐지하기 위한 방어 자료(`detections/`)** 를 함께 제공함 (Sigma·Suricata·ATT&CK·침해지표·호스트 triage)

---

## 1. 전체 시스템 아키텍처

### 1.1 계층 구조

- **프런트엔드 계층 (`web/`)**
  - Next.js 16(App Router) + React 19 + TypeScript, shadcn/ui·Radix·Tailwind v4 기반
  - 정적 빌드(`output: export`) 후 `go:embed` 로 Go 바이너리에 내장 → 단일 실행 파일 배포
- **서버 계층 (`server/`)**
  - Go 표준 `net/http` 라우터, JWT 인증, SSE 스트리밍
  - `Manager` 가 작업·엔진·DB 스토어 생명주기를 관리함
- **에이전트 계층 (`agent/`, norma SDK)**
  - 역할별 에이전트: goals / planner / worker / mainagent (+ 커스텀 auto·pentest·reporter)
- **데이터 계층 (PostgreSQL + SQLite 사이드카)**
  - PostgreSQL: 회사·자산·탐색 그래프·작업·범위·취약점·규칙·알림의 시스템 오브 레코드
  - SQLite: HTTP 트래픽 기록 전용 사이드카(인덱스 + 블롭)
- **지원 서브시스템**
  - 기록형 MITM 트래픽 프록시 / 도구 호출 승인 게이트(guard·intercept) / 비동기 자산 보강(enrich) / MCP·스킬·메모리·리포트

### 1.2 기동·배포 모델

- 진입점 `cmd/artex/main.go` 는 서브커맨드 없이 플래그만 노출함
  - `-addr`(기본 `:8787`): 관리 UI·REST API 리슨 주소
  - `-proxy`(기본 `127.0.0.1:8788`): 트래픽 기록 MITM 프록시
  - `-data`(기본 `<baseDir>/data`): 로컬 상태 디렉터리(`jwt.key`·트래픽·아카이브 등)
- 기동 순서: 플래그 파싱 → 로그 캡처 시작 → 자가 업데이트 부트스트랩 → 설정 경로 해석 → Manager 생성 → HTTP 서버 기동
- 배포 방식 두 가지
  - **Docker Compose**: `postgres:16-alpine` + `artex` 이미지(현재 compose 기본은 상류 중국어 이미지)
  - **단일 바이너리**: `build.sh` 가 OS/arch 별 `-tags embedui` 바이너리 생성, `start.sh` 감독 루프로 구동
- **기동이 곧 마이그레이션**: 매 부팅마다 `schema.sql` 을 멱등하게 재적용함(`ADD COLUMN/CREATE INDEX IF NOT EXISTS`)

---

## 2. 에이전트 역할 구조 (핵심 자율성)

### 2.1 네 가지 기본 역할

- **goals (목표 분해자)** — `agent/goals.go`
  - 1회성 실행(최대 8턴), 작업 목표·설명을 검증 가능한 goal 노드로 분해함
  - 운영 제약(`set_constraints`)과 자산 범위(`add_task_scope`)를 먼저 추출함
  - 최소 범위 원칙: 정확한 호스트만 등록하고 루트 도메인으로 확장하지 않음
- **planner (계획자)** — `agent/planner.go`
  - **유일한 의도(intent) 생성자**, 이벤트 구동(그래프 변경 debounce 또는 하트비트)으로 깨어남
  - 상황을 읽고 목표를 판정하며, 아직 커버하지 못한 방향에만 의도를 frontier 로 배정함
  - 작업마다 보존되는 **공유 todolist** 로 직렬 공격 체인을 라운드에 걸쳐 한 단계씩 전개함
- **worker (실행자) ×N** — `agent/worker.go`
  - **의도 하나**를 수령해 실제 도구(Bash·Kali 도구·HTTP)로 실행하고, 결과를 두 그래프에 써 넣은 뒤 멈춤
  - 세 가지 쓰기 도구 분리: `insert_assets`(자산 그래프)·`record_fact`(탐색 그래프)·`report_finding`(재현 가능 PoC 가 있을 때만)
  - 의도는 토큰 비용을 감수하고 **시스템 프롬프트에 고정**하여 압축(compaction)에도 소실되지 않게 함
- **mainagent (사람 개입 오케스트레이터)** — `agent/mainagent.go`
  - 탐색·의도 생성을 하지 않음. 사람이 대화로 조종하고, 에이전트는 관측·조타만 함
  - `add_hint`·`add_intent`·`steer_work`·`set_goals`·`set_constraints` 등으로 실행 중 개입함

### 2.2 프롬프트·도구를 데이터로 관리

- 에이전트 프롬프트 본문과 도구 바인딩은 **DB 에 저장되고 기동 시 버전 마이그레이션**됨(UI 편집 가능)
- 코드가 소유하는 것은 도구 핸들러와 편집 불가 프롬프트 꼬리뿐임(산출물 규약·트래픽 도구 블록·언어 지시문)
- `agent/toolcatalog.go` 의 `ToolResolve` 훅이 에이전트별 바인딩을 필터링하고 DB 오버라이드를 주입함

### 2.3 현지화(언어) 메커니즘

- `agent/prompt.go` 의 `langDirective()` 가 **최고 우선순위 출력 언어 규약**을 주입함
- 방침: 내부 추론은 원문 유지(성능 보존), 사용자 노출 자연어 필드만 한국어 강제
- 명령·payload·코드·URL·파라미터명·로그/응답 원문은 **원형 그대로 보존**(증거·재현성 보호)

---

## 3. 이중 그래프 블랙보드 모델

### 3.1 자산 그래프 (전역 공유, 진실 저장소)

- 작업을 가로질러 공유하는 자산 사실 저장소임
- 단일 `assets` 테이블이 `root_domain/subdomain/ip/service/app/endpoint` 유형을 모두 수용함
- 부모·자식 관계, 중복 제거 키는 프로그램이 계산하고 에이전트는 원본 정보만 제출함
- `company_scope`·`task_scope` 가 인가 범위이자 커버리지 분모 역할을 함

### 3.2 탐색 그래프 (작업마다 독립, 진행 체인)

- 한 작업의 "사고와 진행" 과정임
- 노드 종류: `begin/goal/intent/fact/finding/hint/digest`(`exploration_nodes`)
- 간선 종류: `spawns/derived_from/yields/proves/covers`(`exploration_edges`)
- `digest` 노드는 식은(cold) 노드를 무손실 접어(fold) 컨텍스트 예산을 관리함(`covers` 간선)

### 3.3 두 그래프의 연결

- `exploration_anchors(node_id, asset_id)` 가 의도·사실·취약점을 구체 자산에 고정함
- 양방향 조회 가능: 탐색 방향 ↔ 공략 자산을 서로 추적할 수 있음

---

## 4. 오케스트레이션 엔진

### 4.1 이벤트 구동 폐곡선 (`server/engine.go`)

- 작업마다 `plannerLoop` 하나 + worker goroutine N 개(기본 3)를 돌림
- 그래프 변경을 약 800ms debounce 로 묶어 한 라운드로 만들고, 하트비트(기본 600s)가 교착 방지 백스톱 역할을 함
- worker 는 `claimNext`(CAS)로 의도를 수령하고, 종료 상태를 세분 라우팅함(done/exhausted/blocked/stopped/paused)
- 실행 중 제어: `kill_work`(강제 종료)·`steer_work`(PreToolUse 훅으로 다음 도구 호출 전 경로 수정)
- 모델/공급자 일시 오류는 백오프와 함께 의도를 최대 2회 재실행함
- 삭제 배리어: 작업 삭제 시 진행 중 planner/worker 실행과 LLM 기록 쓰기를 모두 드레인한 뒤 데이터 제거함

### 4.2 교차 작업 오케스트레이션 (`server/orchestration.go`)

- "auto" 에이전트가 플랫폼을 조작하는 호스트 도구 제공: `spawn_task`·`pause_task`·`get_task_graph`·`add_task_hint` 등
- `report_finding` 때마다 깨어나 Markdown 리포트를 쓰는 내장 "보고서 작성(reporter)" 에이전트를 시드함

### 4.3 스케줄러 (`server/scheduler.go`)

- 5초 틱으로 트리거 구동: interval·on_finding·on_goal_met·on_task_timeout·on_tool_call·on_task_create
- 단조 워터마크 + 발화 집합 영속화로 재시작 시 중복 발화를 방지함

---

## 5. 안전·통제 계층

### 5.1 Guard 훅 (`guard/guard.go`)

- 모든 도구 호출이 `PreToolUse`·`PostToolUse` 훅을 통과함(감사 로그 + 통제)
- 과거의 RoE 인가 범위 메커니즘과 하드코딩 파괴/반출 게이트는 제거됨 → 통제가 **DB intercept 규칙으로 이전**됨
- 차단 메시지는 "ARTEX 플랫폼 관제·비대상 방어" 프레이밍을 써서 에이전트가 WAF/403 처럼 우회하려 들지 않고 전략을 바꾸도록 유도함(프롬프트 엔지니어링)
- `postToolUse` 는 Bash 결과를 blocked/error/ok 로 분류해 planner 가 전략을 조정하게 함

### 5.2 도구 호출 인터셉트 (`intercept/` · `db/intercept.go`)

- 규칙은 DB(`intercept_rules`)에서 로드·캐시되고 우선순위로 평가됨
- 동작: **allow / deny / ask**(ask 는 `intercept_pending` 생성 후 사람 결정까지 블록)
- 규칙 미매치 시 선택적으로 LLM 판정자(judge)가 자동 판단함

### 5.3 기본 시드 안전 규칙 (`db/db.go`)

- 최초 기동 시 내장 안전 규칙을 시드하되, 설정 플래그로 게이트하여 사용자 편집을 덮어쓰지 않음
- 시스템 파괴 명령(우선순위 100): `rm -rf`·시스템 경로 삭제·`mkfs`·`dd of=/dev/*`·fork bomb·shutdown/reboot·`kill -9 -1`·`shred/wipe`·방화벽 초기화
- DB 파괴 조작(우선순위 90) 등 추가 규칙 포함
- 특성: 사용자 설정 가능하며, 매칭 규칙이 없고 judge 가 꺼져 있으면 **기본 allow** 임(방어 관점 유의점)

### 5.4 자산 레벨 인터셉트

- `asset_intercept_rules`(전역)·`task_intercept_rules`(작업별)가 대상 자산을 block/allow 로 통제함
- 기본 시드에 `.gov`·`.gov.cn`·`.edu`·`.edu.cn` 퍼지 도메인 차단이 포함됨

---

## 6. LLM 공급자 스택 (계층 데코레이터)

- 역할별 우선순위: 에이전트 바인딩 → 작업 프로필 체인 → 전역/환경 공급자
- **llmpool** (`llmpool/`): 프로필 체인 페일오버 + 프로세스 전역 서킷 브레이커(1분/5분/30분 백오프)
  - 출력이 나오기 전 "안전 창"에서만 페일오버(중간 스트림 실패는 중복 출력 방지 위해 표면화)
- **task_llm** (`server/task_llm.go`): 작업별 페일오버 상태 머신, 쿼터 소진 시 프로필 체인 전진 + 감사 활동 기록
- **llmretry** (`server/llmretry.go`): 5계층 재시도 정책(연결·빈응답·안전창·브레이커·의도 재생)
- **llmrec** (`llmrec/`): 토큰 사용량은 항상 계측, 전체 요청/응답 본문 기록은 `llm_record` 설정으로 게이트
- OpenAI 호환 엔드포인트(국산·오픈 모델)로 공급자 교체 가능 — 국내 스택 대응 목적

---

## 7. 데이터 지속 계층 (`db/`)

- 핵심 타입은 `type DB struct{ *sql.DB }`(pgx 백엔드), `schema.sql`(약 1,390행·40여 테이블) 내장 적용
- 주요 테이블 군
  - 회사·자산·범위: `companies`·`assets`·`company_scope`·`task_scope`
  - 탐색 그래프: `explorations`·`exploration_nodes`·`exploration_edges`·`exploration_anchors`·`activity`
  - 작업: `tasks`(exploration 과 1:1)·`task_archives`(콜드 스토리지 `.tar.zst`)·`task_templates`
  - 취약점: `findings`(작업 삭제에도 생존, `ON DELETE SET NULL`)·`finding_retests`
  - 통제: `intercept_rules`·`intercept_pending`·`asset_intercept_rules`·`task_intercept_rules`
  - 증거: `traffic_evidence_snapshots`·`finding_traffic_bindings`
  - 알림: `notification_channels`·`notification_events`·`notification_deliveries`
- 취약점은 **그래프 노드(kind=finding)와 영속 행**으로 이중 저장되어 작업 삭제 후에도 보존됨

---

## 8. 트래픽 기록 vs 증거 보존

### 8.1 기록형 MITM 프록시 (`traffic/traffic.go`)

- 내장 go-mitmproxy 프록시가 모든 대상 HTTP 교환을 **평문·무편집**으로 기록함(대상 HTTP 한정)
- SQLite 사이드카 인덱스(`exchanges`·`exchange_bodies`·`blob_refs`) + 트라이그램 FTS5(CJK 본문 검색)
- MITM CA 를 `<dir>/_ca/mitmproxy-ca-cert.pem` 에 생성 → 호스트 포렌식 탐지 아티팩트가 됨
- worker 의 모든 HTTP(Bash 하위 명령·WebFetch)는 `proxyEnv` 로 생태계별 CA 신뢰 환경변수를 설정해 프록시를 경유함

### 8.2 증거 저장소 (`evidence/store.go`)

- 사라질 수 있는 트래픽과 **독립적으로** 취약점 증거를 보존함
- 콘텐츠 주소화 블롭 저장(SHA-256 검증) + 미참조 본문 유예 후 GC
- 취약점에 트래픽 교환을 고정하면 요청/응답 헤드 + 해시 본문 스냅샷을 복사해 트래픽 삭제 후에도 증거가 생존함

---

## 9. 자산 보강 (`enrich/enrich.go`)

- AI 가 아닌 엔진 측 자산 자동 보완 워커 풀임
- DNS 해석(dnsx)과 웹 자산 HTTP 프로빙을 수행하며 **기록 프록시를 경유**함
- 인바운드 프로브 UA 는 `artex-enrich/1.0` 로, 방어 탐지에서 가장 많이 참조되는 지표임
- DNS 는 무게이트, HTTP 는 교전 규칙(RoE)으로 게이트됨

---

## 10. 확장 도구 표면 (주의 필요)

- **커스텀 도구** (`server/customtool.go`): command/script(Python)/http/shell 종류를 DB 에 저장, 호스트 도구로 주입
  - 설계상 서버에서 임의 셸/Python/HTTP 를 실행하는 강력한 RCE-by-design 표면임
- **플랫폼 도구** (`server/platform_tools.go`): LLM 이 자기 플랫폼을 자가 수정하는 도구
  - `create_skill`·`create_custom_tool`·`create_mcp`·`delete_assets_by_host` 등
  - "플랫폼에 없는 도구는 직접 설치하라"고 권장하는 설명 → 에이전트의 자가 확장 유도
- **원격 MCP 클라이언트** (`mcphttp/client.go`): HTTP/JSON-RPC MCP 서버를 `tool.CoreTool` 로 어댑트(`mcp__server__tool`)
- **스킬** (`skills/`): api-recon(프런트엔드 API 정찰)·playwright-cli(브라우저 자동화)·scopesentry(스캔 플랫폼 연동)

---

## 11. 알림·리포트

- **notify/** 는 stdlib 전용 리프 패키지(DB·서버를 모름), 6종 채널(dingtalk·feishu·wecom·webhook·telegram·email)
  - `mask.go`: 자격 증명 마스킹(`__masked__` 접두 + 말미 6자 힌트), 미지 종류는 fail-closed
  - `filter.go`: 필터 파싱은 절대 에러 없이 "전체 매치"로 저하(과알림 허용 설계), 쓰기 경로에서만 검증
- **알림 테이블 3분리(폭발 반경 설계)**: 채널 설정 / 이벤트(작업 락 안 blind INSERT) / 배달(트랜잭션 밖 팬아웃)
- **report/** 는 확정 finding 노드에서 한국어 침투 리포트(Markdown) 및 내보내기(MD/CSV)를 렌더링함

---

## 12. 방어·탐지 자료 (`detections/`) — 저장소의 방어적 성격

- 모든 지표가 추정이 아니라 **본 저장소 소스에서 확인한 문자열·행동**에 근거함
- **Sigma(호스트/로그)**
  - 원자 규칙: enrich UA(`artex-enrich/1.0`)·자가업데이트 UA(`artex-selfupdate`)·guard 통제 마커·MITM CA 파일·파괴 명령 헌팅
  - 상관 규칙: enrich 스캔 속도·팬아웃·guard 차단 버스트·guard 마커→파괴 명령(temporal)
- **Suricata(네트워크)**: sid 1000001~1000003 (`artex-enrich/`·고속 변형·norma WebFetch `norma/` UA)
- **indicators/**: CSV + MISP 피드(로그·네트워크로 못 보는 호스트/DB 전용 지표 포함 — 포트 8787·프록시 8788·PG 스키마)
- **attack/**: MITRE ATT&CK 내비게이터 레이어(8 기법/6 전술)
- **triage/**: 의심 호스트 한 대에서 돌리는 읽기 전용 "여기서 ARTEX 가 돌았는가" 분류 스크립트
- **tests/**: Sigma lint·match·backends, Suricata `-T`, indicators, misp, attack, triage 자가검증 CI 하네스

---

## 13. 방어 관점 핵심 시사점

- **정적 지문은 바뀔 수 있음**: UA·포트 등은 운영자가 변경 가능 → 부재가 안전을 뜻하지 않음
- **행동 기반 탐지가 더 견고함**: 보강 프로브의 속도·폭(fan-out), 다단계 공격 체인 패턴에 집중할 것
- **MITM CA 아티팩트·리슨 포트·PG 탐색 스키마**는 호스트 포렌식의 강한 단서임
- **기본 allow 통제 정책**: intercept 규칙 미매치 시 기본 허용 → 운영 시 규칙·judge 활성화 검토 필요
- **임의 코드 실행 표면**(custom/platform tools)의 존재 → 플랫폼 호스트 자체의 격리·감사 필수

---

## 14. 분석의 한계 (정직 고지)

- 본 보고서는 **소스 코드 정적 검토** 결과이며, 실제 런타임·동적 분석(바이너리 실행·트래픽 관측)은 수행하지 않음
- 행 번호·파일 경로는 분석 시점 작업 트리 기준이며, 상류 변경에 따라 달라질 수 있음
- 탐지 규칙의 유효성은 저장소가 제공한 CI 하네스 기준 설계이며, 실제 SIEM/센서 환경에서의 발화는 각 환경에서 검증 필요함

---

### 🔍 검증 상태 및 가이드
- **현재 상태**: 정적 검토 완료 (실제 실행 미검증)
- **검증용 테스트 코드/명령어**:
  - 저장소 테스트 스위트: `cd /home/user/artex-ko && go test ./...`
  - 탐지 하네스 자가검증: `bash detections/tests/run-all.sh`
  - 문서 링크 정합성: `python3 -I scripts/check-doc-links.py`
- **예상되는 정상 결과(Output)**:
  - `go test ./...` → 각 패키지 `ok` 출력, 비정상 종료 없음
  - `detections/tests/run-all.sh` → 각 하위 검사 PASS, 종료 코드 0
  - `check-doc-links.py` → EXIT 0 (끊긴 앵커 없음)
