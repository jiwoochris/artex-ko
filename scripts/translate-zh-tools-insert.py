#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Korean-ize tool descriptions/schemas in agent/tools_insert.go and
agent/tools_digest.go.

Only string literals containing Han characters are replaced; Chinese code
comments are left untouched. Every Go escape (`\\n`) and format verb (%d/%s/%q/%w/%v)
is preserved verbatim, and enum values / JSON keys / DSL fragments are kept as-is.

Usage:
  python3 scripts/translate-zh-tools-insert.py [--check]
"""
import pathlib
import re
import sys

PAIRS = [
    ("(未知)", "(알 수 없음)"),
    ("批量登记新发现的资产，一次可混合多种类型（type 见枚举）。\\n", "새로 발견한 자산을 일괄 등록하며, 한 번에 여러 유형을 혼합할 수 있습니다(type 은 열거형 참조).\\n"),
    ("各类型必填字段：root_domain→domain；ip→ip（须为 IPv4/IPv6，非主机名）；subdomain→domain；app→app_name；service(HTTP)→url；service(非HTTP)→service_name+port（ip/domain 至少填一个）；endpoint→url+method。其余字段含义见各自说明。\\n", "유형별 필수 필드: root_domain→domain; ip→ip(IPv4/IPv6 여야 하며 호스트 이름 불가); subdomain→domain; app→app_name; service(HTTP)→url; service(비HTTP)→service_name+port(ip/domain 중 최소 하나); endpoint→url+method. 나머지 필드의 의미는 각 설명을 참조하세요.\\n"),
    ("auth/technologies/params 为追加合并(append)，不覆盖原值。\\n", "auth/technologies/params 는 추가 병합(append)되며 기존 값을 덮어쓰지 않습니다.\\n"),
    ("返回：{results:[{index,id,type}], errors:[{index,error}]}", "반환: {results:[{index,id,type}], errors:[{index,error}]}"),
    ("资产数组，每个元素对应一条资产记录", "자산 배열로, 각 요소가 자산 기록 하나에 대응합니다"),
    ("资产类型", "자산 유형"),
    ("根域名或子域名（root_domain/subdomain 必填）", "루트 도메인 또는 하위 도메인(root_domain/subdomain 필수)"),
    ("ICP 备案号（可选）", "ICP 등록번호(선택)"),
    ("DNS 解析类型：A/AAAA/CNAME/MX 等（subdomain 可选）", "DNS 해석 유형: A/AAAA/CNAME/MX 등(subdomain 선택)"),
    ("DNS 解析值列表（subdomain 可选，如 [\\\"1.2.3.4\\\",\\\"2.3.4.5\\\"]）", "DNS 해석 값 목록(subdomain 선택, 예: [\\\"1.2.3.4\\\",\\\"2.3.4.5\\\"])"),
    ("IP 地址，必须是 IPv4/IPv6 地址，不能填主机名（主机名请用 type=subdomain 的 domain 字段）；ip 类型必填；service/endpoint 类型可填，用于关联 IP", "IP 주소. IPv4/IPv6 주소여야 하며 호스트 이름은 넣을 수 없습니다(호스트 이름은 type=subdomain 의 domain 필드 사용). ip 유형 필수, service/endpoint 유형은 IP 연결을 위해 선택 입력 가능"),
    ("该 IP 绑定的域名列表（ip 类型可选）", "이 IP 에 바인딩된 도메인 목록(ip 유형 선택)"),
    ("开放端口列表（ip 类型可选）", "열린 포트 목록(ip 유형 선택)"),
    ("端口号", "포트 번호"),
    ("服务名称，如 http/ssh/mysql 等（可选）", "서비스 이름, 예: http/ssh/mysql 등(선택)"),
    ("应用名称（app 类型必填）", "애플리케이션 이름(app 유형 필수)"),
    ("Bundle ID（app 类型可选）", "Bundle ID(app 유형 선택)"),
    ("应用分类（可选）", "애플리케이션 분류(선택)"),
    ("应用描述（可选）", "애플리케이션 설명(선택)"),
    ("应用 ICP 备案（可选）", "애플리케이션 ICP 등록(선택)"),
    ("归属企业 id（app 类型可选；app 无法靠 scope 自动归因，需显式指定。id 由 add_company_scope 返回）", "귀속 기업 id(app 유형 선택; app 은 scope 로 자동 귀속되지 않아 명시적으로 지정해야 합니다. id 는 add_company_scope 가 반환)"),
    ("完整 URL，含协议和端口（HTTP 服务必填；service_type 自动设为 http）", "전체 URL(프로토콜과 포트 포함, HTTP 서비스 필수; service_type 은 자동으로 http 설정)"),
    ("HTTP 响应状态码，如 200/301/403/404（可选）", "HTTP 응답 상태 코드, 예: 200/301/403/404(선택)"),
    ("HTTP 响应体字节数（可选）", "HTTP 응답 본문 바이트 수(선택)"),
    ("页面 <title> 内容（可选）", "페이지 <title> 내용(선택)"),
    ("favicon MMH3 哈希（可选）", "favicon MMH3 해시(선택)"),
    ("指纹/技术栈列表，如 [\\\"Nginx\\\",\\\"Vue\\\",\\\"Bootstrap\\\"]（可选）", "핑거프린트/기술 스택 목록, 예: [\\\"Nginx\\\",\\\"Vue\\\",\\\"Bootstrap\\\"](선택)"),
    ("发现的认证信息列表，每条含 type/username/password 等字段（可选，追加不覆盖）", "발견된 인증 정보 목록으로 각 항목은 type/username/password 등 필드를 포함합니다(선택, 추가 병합이며 덮어쓰지 않음)"),
    ("服务名称，如 ssh/mysql/redis（service 非 HTTP 时必填）", "서비스 이름, 예: ssh/mysql/redis(service 가 비HTTP 일 때 필수)"),
    ("端口号（service 非 HTTP 时必填）", "포트 번호(service 가 비HTTP 일 때 필수)"),
    ("HTTP 方法：GET/POST/PUT/PATCH/DELETE 等（endpoint 必填）", "HTTP 메서드: GET/POST/PUT/PATCH/DELETE 등(endpoint 필수)"),
    ("请求参数列表，每条含 location(query/body/header/path)/name/value/type（可选，追加不覆盖）", "요청 파라미터 목록으로 각 항목은 location(query/body/header/path)/name/value/type 을 포함합니다(선택, 추가 병합이며 덮어쓰지 않음)"),
    ("insert_assets 未启用: AssetStore 未初始化", "insert_assets 비활성: AssetStore 가 초기화되지 않았습니다"),
    ("资产 %s %s，已禁止插入", "자산 %s %s 은(는) 삽입이 금지되었습니다"),
    ("Agent 通过 insert_assets 登记", "Agent 가 insert_assets 로 등록"),
    ("Worker 意图 #%d 通过 insert_assets 登记", "Worker 의도 #%d 이 insert_assets 로 등록"),
    ("把域名/IP/CIDR/ICP备案/企业关键词加入某公司的【资产范围】——域名、网络和ICP会自动认领命中的资产，关键词只提供给Agent作为范围提示。\\n", "도메인/IP/CIDR/ICP 등록/기업 키워드를 특정 기업의【자산 범위】에 추가합니다 — 도메인, 네트워크, ICP 는 일치하는 자산을 자동으로 인수하며, 키워드는 Agent 에게 범위 힌트로만 제공됩니다.\\n"),
    ("公司名唯一：company 不存在则新建，已存在则复用(只把范围并进去)。\\n", "기업명은 고유합니다: company 가 없으면 새로 만들고, 있으면 재사용합니다(범위만 병합).\\n"),
    ("scope 一行一条，系统自动识别：根域名 / URL / 单个 IP / CIDR 网段 / ICP备案 / 企业关键词。\\n", "scope 는 한 줄에 하나씩이며 시스템이 자동 인식합니다: 루트 도메인 / URL / 단일 IP / CIDR 대역 / ICP 등록 / 기업 키워드.\\n"),
    ("务必给 reason 说明归属依据(whois/证书/ASN 等)。\\n", "reason 에 귀속 근거(whois/인증서/ASN 등)를 반드시 기재하세요.\\n"),
    ("护栏：拒绝裸 TLD 与过宽网段(IPv4前缀需为/16-/32、IPv6前缀需为/32-/128)，非法行会被跳过并在 errors 返回。", "가드레일: 단독 TLD 와 과도하게 넓은 대역은 거부합니다(IPv4 프리픽스는 /16-/32, IPv6 프리픽스는 /32-/128 필요). 잘못된 줄은 건너뛰고 errors 로 반환합니다."),
    ("公司名(不存在则新建、存在则复用；名称唯一)", "기업명(없으면 생성, 있으면 재사용; 이름 고유)"),
    ("资产范围，一行一条：域名 / URL / IP / CIDR / ICP备案 / 企业关键词", "자산 범위, 한 줄에 하나: 도메인 / URL / IP / CIDR / ICP 등록 / 기업 키워드"),
    ("归属依据(证据/来源)，务必填写", "귀속 근거(증거/출처)를 반드시 기입하세요"),
    ("公司图标 URL(可选；仅新建公司时生效)", "기업 아이콘 URL(선택; 기업을 새로 만들 때만 적용)"),
    ("add_company_scope 未启用: CompanyStore 未初始化", "add_company_scope 비활성: CompanyStore 가 초기화되지 않았습니다"),
    ("company 不能为空", "company 는 비워 둘 수 없습니다"),
    ("创建/获取公司失败: ", "기업 생성/조회 실패: "),
    ("把测试范围加入【本任务】——这是本任务的授权边界，也是资产测试覆盖度的分母。\\n", "테스트 범위를【이 작업】에 추가합니다 — 이것은 이 작업의 인가 경계이며 자산 테스트 커버리지의 분모입니다.\\n"),
    ("kind 支持：company(整个公司名下资产) / root_domain(整个根域，含所有子域) / subdomain(单个精确子域) / ip / cidr / icp / keyword。\\n", "kind 지원: company(기업 전체 명의 자산) / root_domain(루트 도메인 전체, 모든 하위 도메인 포함) / subdomain(정확한 하위 도메인 하나) / ip / cidr / icp / keyword.\\n"),
    ("说明：worker 逐个碰到的主机会被系统【自动】加进范围(精确子域)；本工具用于【主动扩大】——把整个根域/整个公司纳入，或补充指定某子域/IP。\\n", "설명: worker 가 개별적으로 마주친 호스트는 시스템이【자동】으로 범위에 추가합니다(정확한 하위 도메인). 이 도구는【능동적 확대】용입니다 — 루트 도메인 전체/기업 전체를 포함하거나 특정 하위 도메인/IP 를 보충 지정합니다.\\n"),
    ("value：company 传公司名或 id(公司须已存在)；root_domain/subdomain 传域名；ip/cidr 传 IP 或网段；icp/keyword 传备案号或企业关键词。\\n", "value: company 는 기업명 또는 id(기업이 이미 존재해야 함); root_domain/subdomain 는 도메인; ip/cidr 는 IP 또는 대역; icp/keyword 는 등록번호 또는 기업 키워드를 전달합니다.\\n"),
    ("务必给 reason 说明依据(可审计)。多条用 entries 数组。", "reason 에 근거를 반드시 기재하세요(감사 가능). 여러 건이면 entries 배열을 사용하세요."),
    ("批量：[{kind, value}]。kind∈company/root_domain/subdomain/ip/cidr/icp/keyword。", "일괄: [{kind, value}]. kind∈company/root_domain/subdomain/ip/cidr/icp/keyword."),
    ("[单条] company / root_domain / subdomain / ip / cidr / icp / keyword", "[단건] company / root_domain / subdomain / ip / cidr / icp / keyword"),
    ("[单条] 公司名或id / 域名 / IP / CIDR / ICP / 关键词", "[단건] 기업명 또는 id / 도메인 / IP / CIDR / ICP / 키워드"),
    ("加入依据(用于审计)，务必填写", "추가 근거(감사용)를 반드시 기입하세요"),
    ("add_task_scope 未启用: AssetStore 未初始化", "add_task_scope 비활성: AssetStore 가 초기화되지 않았습니다"),
    ("add_task_scope 需要任务上下文(当前无 task)", "add_task_scope 은 작업 컨텍스트가 필요합니다(현재 task 없음)"),
    ("查询【本任务及直接关联任务】范围内、还没被事实锚点覆盖的资产（关联范围只读，供你自己判断要不要补测，不代替你决策）。\\n", "【이 작업 및 직접 연관 작업】범위 내에서 아직 사실 앵커로 커버되지 않은 자산을 조회합니다(연관 범위는 읽기 전용으로, 추가 테스트 여부를 스스로 판단하는 참고용이며 결정을 대신하지 않음).\\n"),
    ("可选按资产类型过滤：root_domain/subdomain/service/app/endpoint/ip。\\n", "자산 유형으로 필터 가능: root_domain/subdomain/service/app/endpoint/ip.\\n"),
    ("分页：page 从 1 起、page_size 默认 10。返回 {assets:[{id,type,label}], total, page, page_size}。仅任务上下文可用。", "페이징: page 는 1 부터, page_size 기본 10. 반환 {assets:[{id,type,label}], total, page, page_size}. 작업 컨텍스트에서만 사용 가능합니다."),
    ("资产类型过滤（可选）：root_domain/subdomain/service/app/endpoint/ip", "자산 유형 필터(선택): root_domain/subdomain/service/app/endpoint/ip"),
    ("页码，从 1 起（默认 1）", "페이지 번호, 1 부터(기본 1)"),
    ("每页数量（默认 10）", "페이지당 건수(기본 10)"),
    ("list_untested_assets 未启用: AssetStore 未初始化", "list_untested_assets 비활성: AssetStore 가 초기화되지 않았습니다"),
    ("list_untested_assets 需要任务上下文", "list_untested_assets 은 작업 컨텍스트가 필요합니다"),
    ("查询资产库：DSL 表达式搜索，或按 id/ids 直取；支持分页。只返回【本任务及直接关联任务】测试范围内的资产。\\n", "자산 라이브러리 조회: DSL 표현식 검색, 또는 id/ids 로 직접 조회; 페이징 지원. 【이 작업 및 직접 연관 작업】테스트 범위 내 자산만 반환합니다.\\n"),
    ("DSL：field=value 模糊(ILIKE) | field==value 精确 | field!=value 排除 | 数字字段支持 > >= < <= | 裸词=全文模糊；AND/OR 组合(AND 优先级高)，可用括号分组。资产类型用独立 type 参数，不写进 DSL。\\n", "DSL: field=value 퍼지(ILIKE) | field==value 정확 | field!=value 제외 | 숫자 필드는 > >= < <= 지원 | 단독 단어=전문 퍼지; AND/OR 조합(AND 우선순위 높음), 괄호로 그룹화 가능. 자산 유형은 별도 type 매개변수를 쓰며 DSL 에 넣지 않습니다.\\n"),
    ("未传 id/ids 时 dsl 必须非空（不允许无条件全量查询）。\\n", "id/ids 를 전달하지 않으면 dsl 은 반드시 비어 있지 않아야 합니다(무조건 전체 조회는 허용되지 않음).\\n"),
    ("可用字段：domain(根/子/服务域名)、root_domain、ip、url、page_title、icp、service_name、app_name、method(如 GET/POST)、service_type(http|other)、record_type(如 A/CNAME)、technology(数组，=模糊 ==精确)、port/status_code/company_id(整数)。\\n", "사용 가능 필드: domain(루트/하위/서비스 도메인), root_domain, ip, url, page_title, icp, service_name, app_name, method(예: GET/POST), service_type(http|other), record_type(예: A/CNAME), technology(배열, =퍼지 ==정확), port/status_code/company_id(정수).\\n"),
    ("示例：status_code>=400 AND technology=shiro ；(port==80 OR port==443) AND technology=nginx", "예: status_code>=400 AND technology=shiro ; (port==80 OR port==443) AND technology=nginx"),
    ("资产类型过滤：root_domain|ip|subdomain|app|service|endpoint（独立字段，可与 dsl 叠加；单独 type 不足以查询，仍需 dsl）", "자산 유형 필터: root_domain|ip|subdomain|app|service|endpoint(별도 필드로 dsl 과 함께 쓸 수 있음; type 만으로는 조회할 수 없고 dsl 이 필요)"),
    ("直接按单个资产 id 取（可选，与 dsl/type 互斥）", "단일 자산 id 로 직접 조회(선택, dsl/type 과 상호 배타)"),
    ("直接按多个资产 id 取（可选，与 dsl/type 互斥）", "여러 자산 id 로 직접 조회(선택, dsl/type 과 상호 배타)"),
    ("返回上限，默认 10（可选）", "반환 상한, 기본 10(선택)"),
    ("分页偏移，默认 0（可选）", "페이징 오프셋, 기본 0(선택)"),
    ("list_assets 未启用: AssetStore 未初始化", "list_assets 비활성: AssetStore 가 초기화되지 않았습니다"),
    ("未传 id/ids 时 dsl 不能为空：不允许无条件查询全部资产，请提供查询条件", "id/ids 를 전달하지 않으면 dsl 은 비워 둘 수 없습니다: 전체 자산 무조건 조회는 허용되지 않으니 조회 조건을 제공하세요"),
    ("DSL 错误: ", "DSL 오류: "),
    ("列出资产库中的【企业/公司】及其资产范围(scope)与已归属资产数。用于查看有哪些公司、", "자산 라이브러리의【기업/회사】와 그 자산 범위(scope), 귀속된 자산 수를 나열합니다. 어떤 기업이 있는지 확인하고, "),
    ("拿到 company_id（insert_assets 关联 app、list_assets 按 company_id 过滤时用）。", "company_id 를 얻는 데 사용합니다(insert_assets 로 app 을 연결하거나 list_assets 를 company_id 로 필터할 때)."),
    ("可选 search 按公司名模糊过滤(不区分大小写)，留空返回全部。", "선택 사항인 search 로 기업명 퍼지 필터(대소문자 구분 없음)를 적용할 수 있으며, 비우면 전체를 반환합니다."),
    ("按公司名模糊过滤(可选，不区分大小写)；留空返回全部", "기업명 퍼지 필터(선택, 대소문자 구분 없음); 비우면 전체 반환"),
    ("list_companies 未启用: CompanyStore 未初始化", "list_companies 비활성: CompanyStore 가 초기화되지 않았습니다"),
    ("查询公司失败: ", "기업 조회 실패: "),
    ("展开一个 cold digest：返回它折叠的成员紧凑列表（id/summary/state/confidence），与概览 recent_facts/recent_done_intents 同形状。要某条完整细节/证据用 node_detail(member_id)。", "콜드 다이제스트 하나를 펼칩니다: 접혀 있던 구성원을 압축 목록으로 반환합니다(id/summary/state/confidence), 개요의 recent_facts/recent_done_intents 와 같은 형태입니다. 특정 항목의 전체 세부/증거는 node_detail(member_id) 를 사용하세요."),
    ("digest 节点 id（来自概览 cold_digests）", "digest 노드 id(개요의 cold_digests 에서 옴)"),
    ("#%d 不是 digest 节点（本任务或直接关联任务里都没找到）", "#%d 은(는) digest 노드가 아닙니다(이 작업 또는 직접 연관 작업 어디에도 없음)"),
]


def main() -> int:
    check = "--check" in sys.argv
    root = pathlib.Path(__file__).resolve().parent.parent
    han = re.compile(r"[\u4e00-\u9fff]")
    files = ["agent/tools_insert.go", "agent/tools_digest.go"]
    applied = 0
    for rel in files:
        path = root / rel
        text = path.read_text(encoding="utf-8")
        before = text
        for zh, ko in sorted(PAIRS, key=lambda p: len(p[0]), reverse=True):
            if zh in text:
                text = text.replace(zh, ko)
                applied += 1
        if not check and text != before:
            path.write_text(text, encoding="utf-8")
    print(f"applied {applied} replacements (check={check})")

    leftover = []
    for rel in files:
        path = root / rel
        for i, line in enumerate(path.read_text(encoding="utf-8").split("\n"), 1):
            if line.strip().startswith("//"):
                continue
            for lit in re.findall(r'"((?:[^"\\]|\\.)*)"', line):
                if han.search(lit):
                    leftover.append(f"{rel}:{i}: {lit[:80]}")
    if leftover:
        print(f"Chinese still present: {len(leftover)}")
        for item in leftover:
            print("  " + item)
    else:
        print("no Chinese left in string literals")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
