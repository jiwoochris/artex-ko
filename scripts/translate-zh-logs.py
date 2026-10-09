#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Korean-ize the SAFE backend surface of the ARTEX Korean edition.

Scope decision (explicit): only strings a human sees -- server log lines that
`server/logsink.go` tees into the dashboard `/api/logs` stream, and API error
messages returned to the UI. Agent/LLM prompts and wire-format tokens are
deliberately NOT touched, because translating them can change model behaviour
and break protocol compatibility.

Verified translation targets:
  - `log.Printf(...)` lines -> captured by logsink and shown in /system/logs
  - `fmt.Errorf(...)` / `errors.New(...)` -> surfaced to the UI as error text
  - DB validation messages -> returned by API handlers

Not translated here (by design):
  - agent/, skills/, prompts/ (model-facing instructions)
  - chat-mention wire tokens (server/chat_mentions.go protocol values)
  - Chinese source comments (developer-facing)

Usage:
  python3 scripts/translate-zh-logs.py           # apply
  python3 scripts/translate-zh-logs.py --check   # report only
"""
import pathlib
import re
import sys

# Longest-first application avoids a short entry splitting a longer literal.
REPLACEMENTS = {
    "traffic/traffic.go": [
        ("[traffic] 索引库未启用增量回收（auto_vacuum=%d）：删除流量不会缩小 index.sqlite，需要执行一次存储压缩来转换", "[traffic] 인덱스 DB 에 증분 회수(auto_vacuum=%d)가 비활성입니다: 트래픽을 삭제해도 index.sqlite 가 줄지 않으므로 저장소 압축을 한 번 실행해 전환해야 합니다"),
        ("[traffic] 全文索引不可用，正文搜索将被禁用（元数据搜索不受影响）：%v", "[traffic] 전문 인덱스를 사용할 수 없어 본문 검색이 비활성화됩니다(메타데이터 검색은 영향 없음): %v"),
        ("解析代理地址 %q: %w", "프록시 주소 파싱 %q: %w"),
        ("代理 %q 缺少协议(用 http://、https:// 或 socks5://)", "프록시 %q 에 프로토콜이 없습니다 (http://, https:// 또는 socks5:// 사용)"),
        ("不支持的代理协议 %q(用 http、https 或 socks5)", "지원하지 않는 프록시 프로토콜 %q (http, https 또는 socks5 사용)"),
        ("代理 %q 缺少主机地址", "프록시 %q 에 호스트 주소가 없습니다"),
        ("[traffic] 与 %s 的 MITM 出错，改为透传（该 host 后续直连目标、不再记录，但请求照常）：%v", "[traffic] %s MITM 오류로 패스스루로 전환합니다(이 호스트는 이후 대상에 직접 연결되고 기록되지 않지만 요청은 정상 처리): %v"),
        ("[traffic] 记录 %s 失败（开启事务）：%v", "[traffic] %s 기록 실패(트랜잭션 시작): %v"),
        ("[traffic] 记录 %s 失败（写索引）：%v", "[traffic] %s 기록 실패(인덱스 쓰기): %v"),
        ("[traffic] 记录 %s 失败（取 rowid）：%v", "[traffic] %s 기록 실패(rowid 조회): %v"),
        ("[traffic] 记录 %s 失败（写正文）：%v", "[traffic] %s 기록 실패(본문 쓰기): %v"),
        ("[traffic] 记录 %s 失败（登记 blob 引用）：%v", "[traffic] %s 기록 실패(blob 참조 등록): %v"),
        ("[traffic] 记录 %s 失败（写全文索引）：%v", "[traffic] %s 기록 실패(전문 인덱스 쓰기): %v"),
        ("[traffic] 记录 %s 失败（提交）：%v", "[traffic] %s 기록 실패(커밋): %v"),
        ("[traffic] 创建 blob 目录失败：%v", "[traffic] blob 디렉터리 생성 실패: %v"),
        ("[traffic] 写 blob %s 失败：%v", "[traffic] blob %s 쓰기 실패: %v"),
        ("非法的 blob hash", "잘못된 blob 해시"),
        ("blob %s 不存在", "blob %s 이(가) 존재하지 않습니다"),
        ("exchange %s 无正文记录", "exchange %s 에 본문 기록이 없습니다"),
        ("提交流量索引删除: %w", "트래픽 인덱스 삭제 커밋: %w"),
        ("[traffic] 压实索引失败：%v", "[traffic] 인덱스 압축 실패: %v"),
        ("合并全文索引: %w", "전문 인덱스 병합: %w"),
        ("压实索引: %w", "인덱스 압축: %w"),
        ("截断 WAL: %w", "WAL 잘라내기: %w"),
        ("删除全文索引: %w", "전문 인덱스 삭제: %w"),
        ("删除正文: %w", "본문 삭제: %w"),
        ("删除 blob 引用: %w", "blob 참조 삭제: %w"),
        ("删除索引行: %w", "인덱스 행 삭제: %w"),
        ("检查历史流量目录 %s: %w", "과거 트래픽 디렉터리 확인 %s: %w"),
        ("创建流量暂存目录: %w", "트래픽 스테이징 디렉터리 생성: %w"),
        ("移出历史流量目录 %s: %w", "과거 트래픽 디렉터리 이동 %s: %w"),
        ("[traffic] 清理历史流量目录 %s 失败：%v", "[traffic] 과거 트래픽 디렉터리 %s 정리 실패: %v"),
        ("回滚流量删除: %w", "트래픽 삭제 롤백: %w"),
        ("读取流量暂存日志 %s: %w", "트래픽 스테이징 로그 읽기 %s: %w"),
        ("流量暂存日志 %s 的版本 %d 不受支持", "트래픽 스테이징 로그 %s 의 버전 %d 은(는) 지원되지 않습니다"),
        ("流量暂存日志包含越界路径: %s", "트래픽 스테이징 로그에 범위를 벗어난 경로가 있습니다: %s"),
        ("流量归档 %d 无状态解析器", "트래픽 아카이브 %d 에 상태 파서가 없습니다"),
        ("回滚流量索引: %w", "트래픽 인덱스 롤백: %w"),
        ("回收流量 blob: %w", "트래픽 blob 회수: %w"),
        ("[traffic] 回收索引空间失败（获取连接）：%v", "[traffic] 인덱스 공간 회수 실패(연결 획득): %v"),
        ("[traffic] 回收索引空间失败：%v", "[traffic] 인덱스 공간 회수 실패: %v"),
        ("[traffic] 索引空间回收未做完（已用满 %d 步上限），下次删除时继续", "[traffic] 인덱스 공간 회수가 끝나지 않았습니다(단계 상한 %d 소진). 다음 삭제 시 계속합니다"),
        ("[traffic] 索引空间回收未做完（已用满 %s 预算），下次删除时继续", "[traffic] 인덱스 공간 회수가 끝나지 않았습니다(예산 %s 소진). 다음 삭제 시 계속합니다"),
        ("[traffic] 截断 WAL 失败：%v", "[traffic] WAL 잘라내기 실패: %v"),
        ("回收索引空闲页: %w", "인덱스 빈 페이지 회수: %w"),
        ("当前实例未启用全文索引，无法按正文搜索", "현재 인스턴스는 전문 인덱스가 비활성이라 본문으로 검색할 수 없습니다"),
        ("正文搜索关键词至少需要 %d 个字符（当前 %d 个）", "본문 검색 키워드는 최소 %d자 이상이어야 합니다(현재 %d자)"),
        ("host 为必填参数", "host 는 필수 매개변수입니다"),
        ("无法解析 host：%q", "host 를 해석할 수 없습니다: %q"),
        ("端口无效：%q", "포트가 유효하지 않습니다: %q"),
        ("host 为必填参数：请指定裸主机、主机:端口或完整 URL，避免全库扫描。", "host 는 필수 매개변수입니다: 전체 스캔을 피하려면 호스트만, 호스트:포트, 또는 전체 URL 을 지정하세요."),
    ],
    "traffic/evidence.go": [
        ("流量录制存储不可用", "트래픽 기록 저장소를 사용할 수 없습니다"),
        ("流量 %s: %w", "트래픽 %s: %w"),
        ("旧流量路径无效", "이전 트래픽 경로가 유효하지 않습니다"),
        ("旧流量报文头过大", "이전 트래픽 메시지 헤더가 너무 큽니다"),
    ],
    "cmd/artex/main.go": [
        ("[config] 配置文件: %s (不存在 — 将仅尝试环境变量 ARTEX_PG_DSN)", "[config] 설정 파일: %s (없음 — ARTEX_PG_DSN 환경 변수만 시도합니다)"),
        ("[config] 配置文件: %s", "[config] 설정 파일: %s"),
        ("[config] skill 目录: %s", "[config] skill 디렉터리: %s"),
    ],
    "evidence/store.go": [
        ("证据正文校验失败: %s", "증거 본문 검증 실패: %s"),
        ("正文不完整: 预期 %d 字节，读取 %d 字节", "본문이 불완전합니다: 예상 %d 바이트, 실제 %d 바이트"),
        ("原始流量正文哈希不匹配", "원본 트래픽 본문 해시가 일치하지 않습니다"),
        ("side 必须为 request 或 response", "side 는 request 또는 response 여야 합니다"),
        ("归档证据快照元数据哈希不匹配", "보관된 증거 스냅샷 메타데이터 해시가 일치하지 않습니다"),
    ],
    "selfupdate/bootstrap.go": [
        ("[update] 跳过自举：%v", "[update] 부트스트랩 건너뜀: %v"),
        ("[update] 暂存的新版本未通过校验，已丢弃，继续运行当前版本：%v", "[update] 스테이징된 새 버전이 검증을 통과하지 못해 폐기하고 현재 버전으로 계속 실행합니다: %v"),
        ("[update] 换装失败，继续运行当前版本：%v", "[update] 교체 실패, 현재 버전으로 계속 실행합니다: %v"),
        ("[update] 写升级标记失败（失去自动回滚能力）：%v", "[update] 업그레이드 마커 쓰기 실패(자동 롤백 기능 상실): %v"),
        ("[update] 已换装到 %s，退出以重启（exit %d）", "[update] %s 로 교체 완료, 재시작을 위해 종료합니다(exit %d)"),
        ("[update] 新版本连续 %d 次启动失败，且回滚失败：%v", "[update] 새 버전이 %d회 연속 시작에 실패했고 롤백도 실패했습니다: %v"),
        ("[update] 新版本连续 %d 次启动失败，已回滚到 %s，退出以重启（exit %d）", "[update] 새 버전이 %d회 연속 시작에 실패해 %s 로 롤백했습니다. 재시작을 위해 종료합니다(exit %d)"),
        ("[update] 更新升级标记失败：%v", "[update] 업그레이드 마커 갱신 실패: %v"),
        ("[update] 新版本启动中（第 %d/%d 次尝试），稳定运行后将确认升级", "[update] 새 버전 시작 중(%d/%d 시도). 안정적으로 실행되면 업그레이드를 확정합니다"),
        ("[update] 清除升级标记失败：%v", "[update] 업그레이드 마커 삭제 실패: %v"),
        ("[update] 新版本运行稳定，升级完成（上一版本保留为 %s）", "[update] 새 버전이 안정적으로 실행되어 업그레이드가 완료되었습니다(이전 버전은 %s 로 보존)"),
        ("[update] 回滚后整理备份失败（不影响运行）：%v", "[update] 롤백 후 백업 정리 실패(실행에는 영향 없음): %v"),
    ],
    "llmpool/pool.go": [
        ("[llmpool] 配置 %q(%s) 已熔断：%s", "[llmpool] 설정 %q(%s) 이(가) 서킷 브레이크되었습니다: %s"),
        ("[llmpool] LLM 故障转移：%q(%s) → %q(%s)，原因：%s", "[llmpool] LLM 장애 조치: %q(%s) → %q(%s), 원인: %s"),
        ("[llmpool] 轮询链已耗尽(%d 个配置全部失败)，最后错误：%s", "[llmpool] 폴링 체인이 소진되었습니다(설정 %d개 모두 실패). 마지막 오류: %s"),
    ],
    "intercept/review_context.go": [
        ("工具参数不是有效 JSON", "도구 매개변수가 유효한 JSON 이 아닙니다"),
    ],
    "enrich/enrich.go": [
        ("[enrich] dnsx 初始化失败，DNS 解析停用：%v", "[enrich] dnsx 초기화 실패로 DNS 확인이 비활성화됩니다: %v"),
        ("[enrich] 队列已满，丢弃任务 kind=%d id=%d", "[enrich] 큐가 가득 차 작업을 버립니다 kind=%d id=%d"),
    ],
    "db/chat_mentions.go": [
        ("分页位置无效，请重新搜索", "페이징 위치가 유효하지 않습니다. 다시 검색하세요"),
        ("不支持的引用类型", "지원하지 않는 참조 유형입니다"),
    ],
    "db/notification_delivery.go": [
        ("投递 %d 不存在或当前状态不允许重发", "전송 %d 이(가) 존재하지 않거나 현재 상태에서 재전송할 수 없습니다"),
    ],
    "db/finding_retests.go": [
        ("本次复测已结束或尚未开始，请从漏洞详情发起新的复测", "이번 재검증은 이미 종료되었거나 아직 시작되지 않았습니다. 취약점 상세에서 새 재검증을 시작하세요"),
        ("verdict 必须为 reproduced / fixed / inconclusive", "verdict 는 reproduced / fixed / inconclusive 여야 합니다"),
        ("summary 与 evidence 不能为空；无法确认时说明实际检查及阻塞原因", "summary 와 evidence 는 비워 둘 수 없습니다. 확정할 수 없으면 실제 확인 내용과 차단 원인을 설명하세요"),
        ("复测结论过长（summary ≤ 16KB，evidence ≤ 128KB）", "재검증 결론이 너무 깁니다(summary ≤ 16KB, evidence ≤ 128KB)"),
    ],
    "db/company_scope.go": [
        ("%q 不是有效的 %s 范围", "%q 은(는) 유효한 %s 범위가 아닙니다"),
        ("ICP 不能为空", "ICP 는 비워 둘 수 없습니다"),
        ("企业关键词不能为空", "기업 키워드는 비워 둘 수 없습니다"),
        ("不支持的范围类型: %s", "지원하지 않는 범위 유형: %s"),
        ("空行", "빈 줄"),
        ("无效 CIDR: %s", "유효하지 않은 CIDR: %s"),
        ("无效 IP: %s", "유효하지 않은 IP: %s"),
        ("主机名为空", "호스트 이름이 비어 있습니다"),
        ("缺少主机名", "호스트 이름이 없습니다"),
        ("域名超过 253 个字符", "도메인이 253자를 초과합니다"),
        ("域名至少需要两个标签", "도메인은 최소 두 개의 레이블이 필요합니다"),
        ("域名标签无效", "도메인 레이블이 유효하지 않습니다"),
        ("域名包含无效字符", "도메인에 유효하지 않은 문자가 있습니다"),
        ("网段过宽(IPv4 需 >= /16): %s", "네트워크 범위가 너무 넓습니다(IPv4 는 >= /16 필요): %s"),
        ("网段过宽(IPv6 需 >= /32): %s", "네트워크 범위가 너무 넓습니다(IPv6 는 >= /32 필요): %s"),
        ("无法识别为有效域名/IP/CIDR: %s", "유효한 도메인/IP/CIDR 로 인식할 수 없습니다: %s"),
        ("IP 段请用 CIDR 表示(如 1.2.3.0/24): %s", "IP 대역은 CIDR 로 표기하세요(예: 1.2.3.0/24): %s"),
        ("不能用裸 TLD 作为范围: %s", "단독 TLD 를 범위로 쓸 수 없습니다: %s"),
    ],
    "db/intercept_execution.go": [
        ("任务已被删除或归档", "작업이 삭제되었거나 보관되었습니다"),
        ("对应会话或执行记录已被删除或不存在", "해당 세션 또는 실행 기록이 삭제되었거나 존재하지 않습니다"),
        ("未找到可唯一关联的原始工具调用；记录可能已删除，或旧审批没有保存关联 ID", "고유하게 연결할 원본 도구 호출을 찾지 못했습니다. 기록이 삭제되었거나 이전 승인이 연관 ID 를 저장하지 않았을 수 있습니다"),
    ],
    "db/notification.go": [
        ("通知渠道不存在", "알림 채널이 존재하지 않습니다"),
        ("限流值不能为负", "속도 제한 값은 음수일 수 없습니다"),
        ("[notify] 序列化推送事件失败 finding=%d: %v", "[notify] 푸시 이벤트 직렬화 실패 finding=%d: %v"),
        ("[notify] 建立保存点失败 finding=%d: %v", "[notify] 저장점 생성 실패 finding=%d: %v"),
        ("[notify] 写入推送事件失败 finding=%d（漏洞记录不受影响）: %v", "[notify] 푸시 이벤트 쓰기 실패 finding=%d (취약점 기록에는 영향 없음): %v"),
        ("[notify] 回滚到保存点失败 finding=%d: %v", "[notify] 저장점으로 롤백 실패 finding=%d: %v"),
        ("序列化通知事件快照失败: %w", "알림 이벤트 스냅샷 직렬화 실패: %w"),
    ],
    "db/exploration.go": [
        ("目标不存在", "대상이 존재하지 않습니다"),
    ],
    "db/asset_dsl.go": [
        ("DSL 语法错误：缺少右括号 ')'", "DSL 문법 오류: 닫는 괄호 ')' 가 없습니다"),
        ("DSL 语法错误：表达式不完整", "DSL 문법 오류: 표현식이 불완전합니다"),
        ("DSL 语法错误：意外的 token '%s'", "DSL 문법 오류: 예상치 못한 토큰 '%s'"),
        ("DSL 语法错误：意外的内容 '%s'", "DSL 문법 오류: 예상치 못한 내용 '%s'"),
        ("task_id 需要整数值: %s", "task_id 는 정수 값이어야 합니다: %s"),
        ("company_id 需要整数值: %s", "company_id 는 정수 값이어야 합니다: %s"),
        ("字段 %s 需要整数值: %s", "필드 %s 은(는) 정수 값이어야 합니다: %s"),
        ("字段 %s 不支持运算符 %s", "필드 %s 은(는) 연산자 %s 을(를) 지원하지 않습니다"),
        ("数组字段 %s 不支持运算符 %s", "배열 필드 %s 은(는) 연산자 %s 을(를) 지원하지 않습니다"),
        ("字符串字段 %s 不支持运算符 %s", "문자열 필드 %s 은(는) 연산자 %s 을(를) 지원하지 않습니다"),
        ("未知字段: %s", "알 수 없는 필드: %s"),
    ],
    "db/finding_traffic.go": [
        ("流量证据已变更，请刷新 후重试", "트래픽 증거가 변경되었습니다. 새로고침 후 다시 시도하세요"),
        ("流量证据已变更，请刷新后重试", "트래픽 증거가 변경되었습니다. 새로고침 후 다시 시도하세요"),
        ("漏洞不存在", "취약점이 존재하지 않습니다"),
        ("流量证据不存在", "트래픽 증거가 존재하지 않습니다"),
        ("traffic_id 不能为空", "traffic_id 는 비워 둘 수 없습니다"),
        ("无效的流量用途 %q", "유효하지 않은 트래픽 용도 %q"),
        ("证据快照元数据哈希不匹配", "증거 스냅샷 메타데이터 해시가 일치하지 않습니다"),
        ("无效的流量用途", "유효하지 않은 트래픽 용도"),
        ("漏洞所属任务与探索记录不匹配", "취약점이 속한 작업과 탐색 기록이 일치하지 않습니다"),
        ("intent_id 必须是本任务的意图（关联任务意图只读）", "intent_id 는 이 작업의 의도여야 합니다(연관 작업의 의도는 읽기 전용)"),
    ],
    "db/side_questions.go": [
        ("当前会话已有旁路问题正在回答", "현재 세션에 이미 응답 중인 부가 질문이 있습니다"),
        ("旁路父会话已删除或归档", "부가 질문의 상위 세션이 삭제되었거나 보관되었습니다"),
        ("同一请求 ID 不能用于不同问题", "같은 요청 ID 를 서로 다른 질문에 사용할 수 없습니다"),
    ],
    "db/task_assets.go": [
        ("%w: 第 %d 条范围无效: %v", "%w: %d번째 범위가 유효하지 않습니다: %v"),
        ("%w: 无效 IP: %s", "%w: 유효하지 않은 IP: %s"),
    ],
    "db/companies.go": [
        ("重新计算企业归属失败: %w", "기업 귀속 재계산 실패: %w"),
    ],
    "db/task_scope.go": [
        ("需要 task_id", "task_id 가 필요합니다"),
        ("value 不能为空", "value 는 비워 둘 수 없습니다"),
        ("company store 未启用", "company store 가 활성화되지 않았습니다"),
        ("company 不存在: %s（先用 list_companies 确认，或建好企业）", "company 가 존재하지 않습니다: %s (list_companies 로 확인하거나 기업을 먼저 생성하세요)"),
        ("无效根域: %s", "유효하지 않은 루트 도메인: %s"),
        ("无效子域: %s", "유효하지 않은 하위 도메인: %s"),
        ("无效 ip/cidr: %s", "유효하지 않은 ip/cidr: %s"),
        ("不支持的 kind: %s（company/root_domain/subdomain/ip/cidr/icp/keyword）", "지원하지 않는 kind: %s (company/root_domain/subdomain/ip/cidr/icp/keyword)"),
    ],
    "db/constraints.go": [
        ("kind 必须是 allow 或 deny", "kind 는 allow 또는 deny 여야 합니다"),
        ("约束不存在", "제약이 존재하지 않습니다"),
    ],
    "server/platform_tools.go": [
        ("资产库未初始化", "자산 저장소가 초기화되지 않았습니다"),
        ("host 不能为空", "host 는 비워 둘 수 없습니다"),
        ("删除失败: ", "삭제 실패: "),
        ("skill 名不合法(小写字母开头，仅字母/숫자/连字符，≤64)", "skill 이름이 유효하지 않습니다(소문자로 시작, 영문/숫자/하이픈만, ≤64)"),
        ("skill 名不合法(小写字母开头，仅字母/数字/连字符，≤64)", "skill 이름이 유효하지 않습니다(소문자로 시작, 영문/숫자/하이픈만, ≤64)"),
        ("description 必填", "description 은 필수입니다"),
        ("skill 已存在: ", "skill 이 이미 존재합니다: "),
        ("skill 名不合法", "skill 이름이 유효하지 않습니다"),
        ("skill 不存在: ", "skill 이 존재하지 않습니다: "),
        ("非法路径: ", "잘못된 경로: "),
        ("key 需小写字母开头，仅含小写字母/数字/下划线", "key 는 소문자로 시작하고 소문자/숫자/밑줄만 포함해야 합니다"),
        ("kind 需为 shell / command / script / http", "kind 는 shell / command / script / http 여야 합니다"),
        ("http 工具必须提供参数 JSON Schema(不能留空)", "http 도구는 매개변수 JSON Schema 를 반드시 제공해야 합니다(비워 둘 수 없음)"),
        ("该 key 已存在: ", "해당 key 가 이미 존재합니다: "),
        ("只能修改自定义工具: ", "사용자 정의 도구만 수정할 수 있습니다: "),
        ("name / transport 必填", "name / transport 는 필수입니다"),
        ("id 必填", "id 는 필수입니다"),
    ],
    "server/finding_traffic.go": [
        ("offset / length 不能为负数", "offset / length 는 음수일 수 없습니다"),
        ("offset 超出正文长度", "offset 이 본문 길이를 초과합니다"),
    ],
    "server/finding_workflow.go": [
        ("finding_id 必须为独立漏洞记录 ID；不是探索节点 ID", "finding_id 는 독립 취약점 기록 ID 여야 합니다. 탐색 노드 ID 가 아닙니다"),
        ("%w：finding_id=%d。证据工具使用独立漏洞记录 ID，请从 list_task_findings / get_task_node_detail 的 finding_id 字段读取；不要传 id / finding_node_id", "%w: finding_id=%d. 증거 도구는 독립 취약점 기록 ID 를 사용합니다. list_task_findings / get_task_node_detail 의 finding_id 필드에서 읽고, id / finding_node_id 를 전달하지 마세요"),
        ("任务不存在", "작업이 존재하지 않습니다"),
        ("当前任务不可读取该漏洞", "현재 작업은 해당 취약점을 읽을 수 없습니다"),
        ("继承漏洞的流量证据只读，请到来源任务修改", "상속된 취약점의 트래픽 증거는 읽기 전용입니다. 원본 작업에서 수정하세요"),
        ("Agent 自动绑定流量已关闭；请在系统设置开启，或使用页面人工绑定。", "Agent 자동 트래픽 연결이 꺼져 있습니다. 시스템 설정에서 켜거나 페이지에서 수동으로 연결하세요."),
        ("补绑需要至少一条已核实的 traffic_refs；无流量无需调用此工具", "재연결에는 최소 한 개의 확인된 traffic_refs 가 필요합니다. 트래픽이 없으면 이 도구를 호출할 필요가 없습니다"),
    ],
    "server/orchestration.go": [
        ("task_id 为必填", "task_id 는 필수입니다"),
        ("task 不存在: ", "task 가 존재하지 않습니다: "),
        ("goal 为必填", "goal 은 필수입니다"),
        ("关联任务最多选择 %d 个", "연관 작업은 최대 %d개까지 선택할 수 있습니다"),
        ("关联任务 id 无效或重复", "연관 작업 id 가 유효하지 않거나 중복되었습니다"),
        ("关联任务 #%d 不存在", "연관 작업 #%d 이(가) 존재하지 않습니다"),
        ("LLM 配置 #%d 不存在或未设置 API Key", "LLM 설정 #%d 이(가) 존재하지 않거나 API Key 가 설정되지 않았습니다"),
        ("finding_id 无效", "finding_id 가 유효하지 않습니다"),
        ("未找到 finding_id=%d 对应的漏洞记录(先用 report_finding 登记)", "finding_id=%d 에 해당하는 취약점 기록을 찾지 못했습니다(먼저 report_finding 으로 등록하세요)"),
    ],
    "server/customtool.go": [
        ("[custom-tool] 自动检测到 python 解释器: %s", "[custom-tool] python 인터프리터 자동 감지: %s"),
        ("未知自定义工具类型: ", "알 수 없는 사용자 정의 도구 유형: "),
        ("command 为空", "command 가 비어 있습니다"),
        ("script code 为空", "script code 가 비어 있습니다"),
        ("未配置且未检测到 python 解释器(在系统配置里设置)", "python 인터프리터가 설정되지 않았고 감지되지도 않았습니다(시스템 설정에서 지정하세요)"),
        ("http url 为空", "http url 이 비어 있습니다"),
        ("请求失败: ", "요청 실패: "),
    ],
    "server/engine.go": [
        ("%w: 意图 %d 不再是 %s 状态", "%w: 의도 %d 은(는) 더 이상 %s 상태가 아닙니다"),
        ("纠偏消息不能为空", "교정 메시지는 비워 둘 수 없습니다"),
        ("意图 %d 当前没有运行中的 work（可能已结束或未被领取）", "의도 %d 에 현재 실행 중인 work 가 없습니다(이미 종료되었거나 아직 할당되지 않았을 수 있음)"),
        ("[activity] task %s 丢弃活动记录(该任务累计第 %d 条) worker=%s kind=%s tool=%s tuid=%s reason=%s summary=%q: %v%s", "[activity] task %s 활동 기록 폐기(해당 작업 누적 %d번째) worker=%s kind=%s tool=%s tuid=%s reason=%s summary=%q: %v%s"),
        ("[activity] task %s 写入第 %d 次重试成功 (worker=%s kind=%s tool=%s)", "[activity] task %s %d번째 재시도 쓰기 성공 (worker=%s kind=%s tool=%s)"),
        ("[activity] task %s 写入失败 (第 %d/3 次, worker=%s kind=%s tool=%s expID=%d): %v", "[activity] task %s 쓰기 실패 (%d/3회, worker=%s kind=%s tool=%s expID=%d): %v"),
        ("[goalless] task %s 收尾落 done 失败: %v", "[goalless] task %s 마무리 done 기록 실패: %v"),
        ("[planner] task %s 规划中…(%s 触发)", "[planner] task %s 계획 중…(%s 트리거)"),
        ("[planner] task %s 规划出错: %v", "[planner] task %s 계획 오류: %v"),
        ("[planner] task %s 判定目标达成: %s", "[planner] task %s 목표 달성 판정: %s"),
        ("[planner] task %s 标记完成落库失败: %v", "[planner] task %s 완료 표시 기록 실패: %v"),
        ("[planner] task %s 规划完成", "[planner] task %s 계획 완료"),
        ("[worker %s] task %s 领取意图 #%d", "[worker %s] task %s 의도 #%d 할당"),
        ("[worker %s] task %s 意图 #%d 领取后回退失败: %v", "[worker %s] task %s 의도 #%d 할당 후 롤백 실패: %v"),
        ("[worker %s] task %s 意图 #%d model_error 收场，%v 后重试 (%d/%d)", "[worker %s] task %s 의도 #%d model_error 종료, %v 후 재시도 (%d/%d)"),
        ("[worker %s] task %s 意图 #%d 暂停状态落库失败: %v", "[worker %s] task %s 의도 #%d 일시정지 상태 기록 실패: %v"),
        ("[worker %s] task %s 意图 #%d 已暂停", "[worker %s] task %s 의도 #%d 일시정지됨"),
        ("[worker %s] task %s 意图 #%d 取消栅栏落库失败: %v", "[worker %s] task %s 의도 #%d 취소 펜스 기록 실패: %v"),
        ("[worker %s] task %s 意图 #%d 已停止，等待取消清理", "[worker %s] task %s 의도 #%d 중지됨, 취소 정리 대기"),
        ("[worker %s] task %s 意图 #%d 任务暂停回退失败: %v", "[worker %s] task %s 의도 #%d 작업 일시정지 롤백 실패: %v"),
        ("[worker %s] task %s 意图 #%d 超时收尾状态落库失败: %v", "[worker %s] task %s 의도 #%d 시간 초과 마무리 상태 기록 실패: %v"),
        ("[worker %s] task %s 意图 #%d 因任务超时收尾结束(exhausted)，写回 %s", "[worker %s] task %s 의도 #%d 작업 시간 초과로 마무리 종료(exhausted), %s 로 기록"),
        ("[worker %s] task %s 意图 #%d 终态停止落库失败: %v", "[worker %s] task %s 의도 #%d 최종 상태 중지 기록 실패: %v"),
        ("[worker %s] task %s 意图 #%d 因任务已完成而取消(stopped)", "[worker %s] task %s 의도 #%d 작업 완료로 취소됨(stopped)"),
        ("[worker %s] task %s 意图 #%d planner 停止落库失败: %v", "[worker %s] task %s 의도 #%d planner 중지 기록 실패: %v"),
        ("[worker %s] task %s 意图 #%d 被终止(stopped)", "[worker %s] task %s 의도 #%d 종료됨(stopped)"),
        ("[worker %s] intent %d 撞步数上限(exhausted)，本次写回 %s", "[worker %s] intent %d 단계 수 상한 도달(exhausted), 이번에는 %s 로 기록"),
        ("[worker %s] intent %d 运行超时(exhausted)，收尾后写回 %s", "[worker %s] intent %d 실행 시간 초과(exhausted), 마무리 후 %s 로 기록"),
        ("[worker %s] task %s 意图 #%d 终态 %s 落库失败: %v", "[worker %s] task %s 의도 #%d 최종 상태 %s 기록 실패: %v"),
        ("[worker %s] task %s 意图 #%d 结束: %s (写回 %s)", "[worker %s] task %s 의도 #%d 종료: %s (%s 로 기록)"),
        ("[work %s] 空转回合(仅思考、无正文无工具)已达续跑上限 %d，放行收场", "[work %s] 공회전 턴(사고만 하고 본문·도구 없음)이 연속 실행 상한 %d에 도달, 종료를 허용합니다"),
        ("[work %s] 空转回合(仅思考、无正文无工具)，注入续跑指令 (%d/%d)", "[work %s] 공회전 턴(사고만 하고 본문·도구 없음), 연속 실행 지시 주입 (%d/%d)"),
    ],
    "server/goals.go": [
        ("[concurrency] task %s 启动失败: %v", "[concurrency] task %s 시작 실패: %v"),
        ("[concurrency] task %s 因 LLM 不可用入队失败: %v", "[concurrency] task %s LLM 사용 불가로 큐 등록 실패: %v"),
        ("[revive] task %s 恢复失败: %v", "[revive] task %s 복구 실패: %v"),
        ("[goals] task %s: LLM 目标拆解无产出，回退为「原始目标作为单目标」", "[goals] task %s: LLM 목표 분해 결과가 없어 '원래 목표를 단일 목표로 사용'으로 폴백합니다"),
    ],
    "server/engine_timeout.go": [
        ("[deadline] task %s 盖章 first_run 失败: %v", "[deadline] task %s first_run 기록 실패: %v"),
        ("[deadline] task %s 首次运行,截止于 %s", "[deadline] task %s 최초 실행, 마감 %s"),
        ("[deadline] task %s 到达超时上限,进入收尾时序", "[deadline] task %s 시간 초과 상한 도달, 마무리 단계로 진입"),
        ("[deadline] task %s drain 超时(%s),硬取消在跑 run", "[deadline] task %s drain 시간 초과(%s), 실행 중 run 을 강제 취소"),
        ("[deadline] task %s 落终态失败: %v", "[deadline] task %s 최종 상태 기록 실패: %v"),
        ("[deadline] task %s 收尾完成,终态=%s", "[deadline] task %s 마무리 완료, 최종 상태=%s"),
        ("[deadline] task %s 收尾时已是终态,保留原状态", "[deadline] task %s 마무리 시점에 이미 최종 상태였으므로 원래 상태를 유지합니다"),
        ("[deadline] task %s 终局规划出错: %v", "[deadline] task %s 종국 계획 오류: %v"),
        ("[deadline] task %s 终局判定目标达成: %s", "[deadline] task %s 종국 목표 달성 판정: %s"),
    ],
    "server/assembly.go": [
        ("[mcp] %s 连接失败: %v", "[mcp] %s 연결 실패: %v"),
        ("[mcp] %s tools/list 失败: %v", "[mcp] %s tools/list 실패: %v"),
        ("[prompts] seed %s 跳过: agent 不存在 (%v)", "[prompts] seed %s 건너뜜: agent 가 존재하지 않습니다 (%v)"),
        ("[prompts] seed %s 失败: %v", "[prompts] seed %s 실패: %v"),
        ("[tools] seed %s 失败: %v", "[tools] seed %s 실패: %v"),
        ("[tools] 读取工具表失败，按代码默认放行: %v", "[tools] 도구 테이블 읽기 실패, 코드 기본값으로 허용합니다: %v"),
    ],
    "server/server.go": [
        ("[engine] task %s 重置 %d 个残留 running 意图为 open", "[engine] task %s 잔여 running 의도 %d개를 open 으로 재설정"),
        ("[task] 新建任务 #%s «%s» 目标: %s", "[task] 새 작업 #%s «%s» 목표: %s"),
        ("[task] #%s 意图 #%d 已重开(重跑)", "[task] #%s 의도 #%d 재개됨(재실행)"),
        ("[task] #%s 批量重开 %d 条 blocked 意图", "[task] #%s blocked 의도 %d개 일괄 재개"),
        ("[seed] task %s: 未能从 %q 解析出目标 host/IP，不创建站点（请手动配置 scope）", "[seed] task %s: %q 에서 대상 host/IP 를 해석하지 못해 사이트를 생성하지 않습니다(scope 를 수동 설정하세요)"),
        ("[seed] task %s: 目标 %q 是 LLM 网关，拒绝作为渗透目标", "[seed] task %s: 대상 %q 은(는) LLM 게이트웨이이므로 침투 대상으로 거부합니다"),
        ("[seed] task %s: 目标站点 %s", "[seed] task %s: 대상 사이트 %s"),
        ("[seed] task %s: 下发种子意图失败: %v", "[seed] task %s: 시드 의도 전달 실패: %v"),
        ("[seed] task %s: 已下发种子意图 #%d", "[seed] task %s: 시드 의도 #%d 전달됨"),
        ("[notify] 状态变更事件未登记 finding=%d %s→%s（状态已更新）", "[notify] 상태 변경 이벤트가 등록되지 않았습니다 finding=%d %s→%s (상태는 갱신됨)"),
        ("[traffic] 下载 blob %s 中断：%v", "[traffic] blob %s 다운로드 중단: %v"),
    ],
    "server/task_archive_package.go": [
        ("[task-archive] 跳过符号链接（归档不支持，不影响其它文件）：%s", "[task-archive] 심볼릭 링크 건너뜀(보관 미지원, 다른 파일에는 영향 없음): %s"),
    ],
    "server/auth.go": [
        ("[auth] JWT key 已从 %s 迁移到 %s（移出可浏览工作区）", "[auth] JWT key 를 %s 에서 %s 로 이동했습니다(탐색 가능한 워크스페이스 밖으로)"),
        ("[auth] 新 JWT key 已写入 %s", "[auth] 새 JWT key 를 %s 에 기록했습니다"),
    ],
    "server/update.go": [
        ("[update] 更新失败：%v", "[update] 업데이트 실패: %v"),
        ("[update] %s → %s 已暂存，即将退出以完成换装", "[update] %s → %s 스테이징 완료, 교체를 마치기 위해 곧 종료합니다"),
        ("[update] 已手动回滚到上一版本，即将退出以完成切换", "[update] 이전 버전으로 수동 롤백했습니다. 전환을 마치기 위해 곧 종료합니다"),
    ],
    "server/notifier.go": [
        ("[notify] 分派事件失败: %v", "[notify] 이벤트 분배 실패: %v"),
        ("[notify] 读取渠道失败: %v", "[notify] 채널 읽기 실패: %v"),
        ("[notify] 领取实时投递失败 channel=%d: %v", "[notify] 실시간 전송 할당 실패 channel=%d: %v"),
        ("[notify] 判断汇总批次失败 channel=%d: %v", "[notify] 요약 배치 판정 실패 channel=%d: %v"),
        ("[notify] 领取汇总批次失败 channel=%d: %v", "[notify] 요약 배치 할당 실패 channel=%d: %v"),
        ("[notify] 标记坏快照投递失败 channel=%s ids=%v: %v", "[notify] 손상 스냅샷 전송 표시 실패 channel=%s ids=%v: %v"),
        ("[notify] 跳过 %d 条快照无法解析的投递 channel=%d", "[notify] 스냅샷을 해석할 수 없는 전송 %d건 건너뜀 channel=%d"),
        ("[notify] 渠道回报送达条数 %d 超过投递数 %d channel=%s，按全部送达处理", "[notify] 채널이 보고한 전송 건수 %d 이(가) 전송 수 %d 을(를) 초과하여 전부 전송된 것으로 처리합니다 channel=%s"),
        ("[notify] 标记已送达失败 channel=%s ids=%v: %v", "[notify] 전송 완료 표시 실패 channel=%s ids=%v: %v"),
        ("[notify] 分段续发排队失败 channel=%s ids=%v: %v", "[notify] 분할 재전송 큐 등록 실패 channel=%s ids=%v: %v"),
        ("[notify] 标记失败状态出错 channel=%s ids=%v: %v", "[notify] 실패 상태 표시 오류 channel=%s ids=%v: %v"),
        ("[notify] 重排投递失败 channel=%s ids=%v: %v", "[notify] 전송 재배치 실패 channel=%s ids=%v: %v"),
        ("[notify] 投递失败 channel=%d kind=%s 永久失败=%d 重试耗尽=%d 待重试=%d: %s", "[notify] 전송 실패 channel=%d kind=%s 영구실패=%d 재시도소진=%d 재시도대기=%d: %s"),
        ("[notify] 汇总批次中跳过无法解析的快照 delivery=%d: %v", "[notify] 요약 배치에서 해석할 수 없는 스냅샷 건너뜀 delivery=%d: %v"),
        ("[notify] 解析资产名失败 finding=%d: %v", "[notify] 자산 이름 해석 실패 finding=%d: %v"),
    ],
    "server/llmpool.go": [
        ("[llmpool] 熔断状态落库失败: %v", "[llmpool] 서킷 브레이크 상태 기록 실패: %v"),
        ("[llmpool] 恢复熔断状态: 配置 #%d 冷却至 %s", "[llmpool] 서킷 브레이크 상태 복구: 설정 #%d 냉각 %s 까지"),
        ("[llmpool] 读取轮询链失败: %v", "[llmpool] 폴링 체인 읽기 실패: %v"),
        ("[llmpool] LLM 轮询已启用，链路(%d): %v", "[llmpool] LLM 폴링 활성화, 체인(%d): %v"),
    ],
    "server/manager.go": [
        ("[pg] 数据库配置来源: %s", "[pg] 데이터베이스 설정 출처: %s"),
        ("归档流量暂存日志缺少任务 ID", "보관 트래픽 스테이징 로그에 작업 ID 가 없습니다"),
        ("[proxy] 全局代理 %q 无效，已忽略: %v", "[proxy] 전역 프록시 %q 이(가) 유효하지 않아 무시했습니다: %v"),
        ("[mcp] browser 代理同步: 读取 MCP 列表失败: %v", "[mcp] browser 프록시 동기화: MCP 목록 읽기 실패: %v"),
        ("[mcp] browser 代理同步失败: %v", "[mcp] browser 프록시 동기화 실패: %v"),
        ("[mcp] browser MCP 已挂捕获代理 %s (CA %s)", "[mcp] browser MCP 에 캡처 프록시 %s 연결됨 (CA %s)"),
        ("[mcp] browser MCP 已移除捕获代理配置", "[mcp] browser MCP 에서 캡처 프록시 설정을 제거했습니다"),
    ],
    "server/server_mgmt.go": [
        ("[task-delete] task %s 读取持久状态失败，使用内存状态恢复屏障: %v", "[task-delete] task %s 영구 상태 읽기 실패, 메모리 상태 복구 장벽을 사용합니다: %v"),
        ("[agents] seed starter prompt for %s 失败: %v", "[agents] %s 초기 프롬프트 seed 실패: %v"),
        ("[agents] 清理 %s 工具绑定失败: %v", "[agents] %s 도구 바인딩 정리 실패: %v"),
        ("[agents] 清理 %s 触发器失败: %v", "[agents] %s 트리거 정리 실패: %v"),
        ("[tools] 读取调用统计失败: %v", "[tools] 호출 통계 읽기 실패: %v"),
        ("[skills] 读取调用统计失败: %v", "[skills] 호출 통계 읽기 실패: %v"),
        ("[mcp] %s 添加后工具发现失败: %v", "[mcp] %s 추가 후 도구 발견 실패: %v"),
    ],
    "server/orchestration.go": [
        ("[custom-tool] 加载失败: %v", "[custom-tool] 로드 실패: %v"),
        ("[tools] 已刷新 orchestration/platform 工具 schema 到代码默认(一次性)", "[tools] orchestration/platform 도구 schema 를 코드 기본값으로 갱신했습니다(1회성)"),
        ("[tools] goal_met 解绑 planner 失败: %v", "[tools] goal_met planner 바인딩 해제 실패: %v"),
        ("[prompts] goals 提示词重刷为新默认失败: %v", "[prompts] goals 프롬프트를 새 기본값으로 재적용 실패: %v"),
        ("[prompts] goals 提示词已追加新默认版本(加入抽操作约束步,一次性)", "[prompts] goals 프롬프트에 새 기본 버전을 추가했습니다(추상 연산 제약 단계 추가, 1회성)"),
        ("[prompts] mainagent 提示词重刷为新默认失败: %v", "[prompts] mainagent 프롬프트를 새 기본값으로 재적용 실패: %v"),
        ("[prompts] mainagent 提示词已追加新默认版本(加入目标达成后反问建目标,一次性)", "[prompts] mainagent 프롬프트에 새 기본 버전을 추가했습니다(목표 달성 후 목표 재생성 질문 추가, 1회성)"),
        ("[prompts] planner 提示词重刷为新默认失败: %v", "[prompts] planner 프롬프트를 새 기본값으로 재적용 실패: %v"),
        ("[prompts] planner 提示词已追加新默认版本(精简重构+克制降级去重+深度优先+否定复核上界,一次性)", "[prompts] planner 프롬프트에 새 기본 버전을 추가했습니다(간소화 재구성+절제된 강등 중복 제거+깊이 우선+부정 재검토 상한, 1회성)"),
        ("[prompts] worker 提示词重刷为新默认失败: %v", "[prompts] worker 프롬프트를 새 기본값으로 재적용 실패: %v"),
        ("[prompts] worker 提示词已追加新默认版本(查上下文段收敛为 list_assets/list_findings,去掉 list_facts/node_detail/asset_neighbors,一次性)", "[prompts] worker 프롬프트에 새 기본 버전을 추가했습니다(컨텍스트 조회 구간을 list_assets/list_findings 로 수렴, list_facts/node_detail/asset_neighbors 제거, 1회성)"),
        ("[reporter] 读取触发器失败: %v", "[reporter] 트리거 읽기 실패: %v"),
        ("[reporter] 升级触发消息失败: %v", "[reporter] 트리거 메시지 승격 실패: %v"),
        ("[reporter] 触发消息已升级为读取并回传 evidence_version", "[reporter] 트리거 메시지를 evidence_version 을 읽어 반환하도록 승격했습니다"),
        ("[reporter] 创建 agent 失败: %v", "[reporter] agent 생성 실패: %v"),
        ("[reporter] seed prompt 失败: %v", "[reporter] seed prompt 실패: %v"),
        ("[reporter] 设置触发运行策略失败: %v", "[reporter] 트리거 실행 정책 설정 실패: %v"),
        ("[reporter] 绑定工具失败: %v", "[reporter] 도구 바인딩 실패: %v"),
        ("[reporter] 创建触发器失败: %v", "[reporter] 트리거 생성 실패: %v"),
        ("[reporter] 已预置「报告撰写」agent + finding 触发器", "[reporter] '보고서 작성' agent + finding 트리거를 미리 구성했습니다"),
        ("[auto] report_finding 默认绑定失败: %v", "[auto] report_finding 기본 바인딩 실패: %v"),
        ("[planner] report_finding 默认绑定失败: %v", "[planner] report_finding 기본 바인딩 실패: %v"),
        ("[planner] list_assets 默认绑定失败: %v", "[planner] list_assets 기본 바인딩 실패: %v"),
        ("[planner] add_company_scope 默认绑定失败: %v", "[planner] add_company_scope 기본 바인딩 실패: %v"),
        ("[worker] add_company_scope 解绑失败: %v", "[worker] add_company_scope 바인딩 해제 실패: %v"),
        ("[worker] %s 从 worker 解绑失败: %v", "[worker] %s 를 worker 에서 바인딩 해제 실패: %v"),
        ("[worker] 回看/详情工具补绑失败: %v", "[worker] 되돌아보기/상세 도구 추가 바인딩 실패: %v"),
        ("[auto] 默认绑定失败: %v", "[auto] 기본 바인딩 실패: %v"),
    ],
    "server/task_llm.go": [
        ("[task-llm] task %s 非流式调用失败,%v 后同 provider 重试 (%d/%d): %v", "[task-llm] task %s 비스트리밍 호출 실패, %v 후 동일 provider 로 재시도 (%d/%d): %v"),
        ("[task-llm] task %s 提交前流失败,%v 后同 provider 重试 (%d/%d): %v", "[task-llm] task %s 제출 전 스트림 실패, %v 후 동일 provider 로 재시도 (%d/%d): %v"),
    ],
    "server/finding_retests.go": [
        ("当前会话未关联复测记录，请从漏洞详情发起复测", "현재 세션에 연결된 재검증 기록이 없습니다. 취약점 상세에서 재검증을 시작하세요"),
    ],
}


def _ordered(pairs):
    return sorted(pairs, key=lambda p: len(p[0]), reverse=True)


def main() -> int:
    check = "--check" in sys.argv
    root = pathlib.Path(__file__).resolve().parent.parent
    han = re.compile(r"[\u4e00-\u9fff]")
    changed = []
    for rel, pairs in REPLACEMENTS.items():
        path = root / rel
        if not path.exists():
            print(f"  MISSING {rel}")
            continue
        text = path.read_text(encoding="utf-8")
        before = text
        for zh, ko in _ordered(pairs):
            text = text.replace(zh, ko)
        if text != before:
            changed.append(rel)
            if not check:
                path.write_text(text, encoding="utf-8")
    print(f"files changed: {len(changed)} (check={check})")
    for c in changed:
        print("  " + c)

    # Report Chinese still present inside log/error calls of the touched files.
    leftover = []
    for rel in REPLACEMENTS:
        path = root / rel
        if not path.exists():
            continue
        for i, line in enumerate(path.read_text(encoding="utf-8").split("\n"), 1):
            s = line.strip()
            if s.startswith("//"):
                continue
            if not ("log." in s or "Errorf" in s or "errors.New" in s):
                continue
            for lit in re.findall(r'"((?:[^"\\]|\\.)*)"', line):
                if han.search(lit):
                    leftover.append(f"{rel}:{i}: {lit}")
    if leftover:
        print(f"Chinese still in log/error calls: {len(leftover)}")
        for item in leftover:
            print("  " + item)
    else:
        print("no Chinese left in log/error calls of the processed files")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
