package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// assetInterceptCandidates 提取一条待插入资产输入项的 域名/IP/URL 候选串，用于资产拦截匹配。
// URL 的 host 会拆出归类，使「只带 URL」的服务/端点资产也能被 域名/IP 规则命中。
func assetInterceptCandidates(item assetInputItem) (domains, ips, urls []string) {
	add := func(dst *[]string, s string) {
		if s = strings.TrimSpace(s); s != "" {
			*dst = append(*dst, s)
		}
	}
	add(&domains, item.Domain)
	for _, d := range item.BoundDomains {
		add(&domains, d)
	}
	add(&ips, item.IP)
	add(&ips, item.ServiceIP)
	add(&urls, item.URL)
	if item.URL != "" {
		if u, err := url.Parse(item.URL); err == nil {
			if h := u.Hostname(); h != "" {
				if net.ParseIP(h) != nil {
					add(&ips, h)
				} else {
					add(&domains, h)
				}
			}
		}
	}
	return domains, ips, urls
}

// assetInputLabel 返回一条待插入资产的简短标识，用于拦截说明消息。
func assetInputLabel(item assetInputItem) string {
	typ := strings.TrimSpace(item.Type)
	var target string
	switch {
	case strings.TrimSpace(item.Domain) != "":
		target = strings.TrimSpace(item.Domain)
	case strings.TrimSpace(item.URL) != "":
		target = strings.TrimSpace(item.URL)
	case strings.TrimSpace(item.IP) != "":
		target = strings.TrimSpace(item.IP)
	case strings.TrimSpace(item.ServiceIP) != "":
		target = strings.TrimSpace(item.ServiceIP)
	default:
		target = "(알 수 없음)"
	}
	if typ != "" {
		return fmt.Sprintf("[%s] %s", typ, target)
	}
	return target
}

// =====================================================================
// Unified asset insertion tools
// =====================================================================

// SetAssetStore wires the asset store and company store onto this ToolSet
// so the insert_assets, add_company_scope, and list_assets tools are active.
func (t *ToolSet) SetAssetStore(as *db.AssetStore, cs *db.CompanyStore) {
	t.as = as
	t.cs = cs
}

// assetInputItem is one element of the insert_assets "assets" array.
type assetInputItem struct {
	Type string `json:"type"` // root_domain|ip|subdomain|app|service|endpoint

	// ---- root_domain / subdomain ----
	Domain      string   `json:"domain"`
	ICP         string   `json:"icp"`
	RecordType  string   `json:"record_type"`
	RecordValue []string `json:"record_value"`

	// ---- ip ----
	IP           string           `json:"ip"`
	BoundDomains []string         `json:"bound_domains"`
	OpenPorts    []db.PortService `json:"open_ports"`

	// ---- app ----
	AppName     string `json:"app_name"`
	BundleID    string `json:"bundle_id"`
	Category    string `json:"category"`
	Description string `json:"description"`
	AppICP      string `json:"app_icp"`
	CompanyID   *int64 `json:"company_id"` // explicit company link (app only; others auto-attribute via scope)

	// ---- service (http) ----
	URL           string           `json:"url"`
	Technologies  []string         `json:"technologies"`
	StatusCode    *int             `json:"status_code"`
	ContentLength *int64           `json:"content_length"`
	PageTitle     string           `json:"page_title"`
	FaviconMMH3   string           `json:"favicon_mmh3"`
	Auth          []map[string]any `json:"auth"`
	ServiceName   string           `json:"service_name"`
	ServiceIP     string           `json:"service_ip"` // optional enrichment IP

	// ---- service (other) ----
	Port  int    `json:"port"`
	Proto string `json:"proto"`

	// ---- endpoint ----
	Method string           `json:"method"`
	Params []map[string]any `json:"params"`
}

// insertAssets is the unified insert_assets agent tool.
func (t *ToolSet) insertAssets() actool.CoreTool {
	return writeTool(
		"insert_assets",
		"새로 발견한 자산을 일괄 등록합니다. 한 번에 여러 유형을 섞을 수 있습니다(type 은 열거값 참고).\n"+
			"유형별 필수 필드: root_domain→domain; ip→ip(IPv4/IPv6 여야 하며 호스트명 아님); subdomain→domain; app→app_name; service(HTTP)→url; service(비HTTP)→service_name+port(ip/domain 중 최소 하나); endpoint→url+method. 나머지 필드 의미는 각 설명 참고.\n"+
			"auth/technologies/params 는 추가 병합(append)이며 기존 값을 덮어쓰지 않습니다.\n"+
			"반환: {results:[{index,id,type}], errors:[{index,error}]}",
		obj(map[string]any{
			// task_id 不暴露给模型：worker 归属哪个 task 由程序经 SetTaskID 权威赋值(见 handler)。
			"assets": map[string]any{
				"type":        "array",
				"description": "자산 배열, 각 요소가 자산 레코드 하나에 대응",
				"items": obj(map[string]any{
					"type": map[string]any{
						"type":        "string",
						"enum":        []string{"root_domain", "ip", "subdomain", "app", "service", "endpoint"},
						"description": "자산 유형",
					},
					// root_domain / subdomain
					"domain":      str("루트 도메인 또는 서브도메인(root_domain/subdomain 필수)"),
					"icp":         str("ICP 등록번호(선택)"),
					"record_type": str("DNS 레코드 유형: A/AAAA/CNAME/MX 등(subdomain 선택)"),
					"record_value": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "DNS 응답값 목록(subdomain 선택, 예 [\"1.2.3.4\",\"2.3.4.5\"])",
					},
					// ip
					"ip": str("IP 주소, IPv4/IPv6 주소여야 하며 호스트명은 안 됨(호스트명은 type=subdomain 의 domain 필드 사용); ip 유형 필수; service/endpoint 유형은 IP 연관용으로 입력 가능"),
					"bound_domains": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "이 IP 에 연결된 도메인 목록(ip 유형 선택)",
					},
					"open_ports": map[string]any{
						"type":        "array",
						"description": "열린 포트 목록(ip 유형 선택)",
						"items": obj(map[string]any{
							"port":    intp("포트 번호"),
							"service": str("서비스 이름, 예 http/ssh/mysql 등(선택)"),
						}, "port"),
					},
					// app
					"app_name":    str("앱 이름(app 유형 필수)"),
					"bundle_id":   str("Bundle ID(app 유형 선택)"),
					"category":    str("앱 분류(선택)"),
					"description": str("앱 설명(선택)"),
					"app_icp":     str("앱 ICP 등록(선택)"),
					"company_id":  intp("소속 기업 id(app 유형 선택; app 은 scope 로 자동 귀속되지 않아 명시 지정 필요. id 는 add_company_scope 가 반환)"),
					// service (http)
					"url":         str("전체 URL, 프로토콜과 포트 포함(HTTP 서비스 필수; service_type 은 자동으로 http 로 설정)"),
					"status_code": intp("HTTP 응답 상태 코드, 예 200/301/403/404(선택)"),
					"content_length": map[string]any{
						"type":        "integer",
						"description": "HTTP 응답 본문 바이트 수(선택)",
					},
					"page_title":   str("페이지 <title> 내용(선택)"),
					"favicon_mmh3": str("favicon MMH3 해시(선택)"),
					"technologies": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "핑거프린트/기술 스택 목록, 예 [\"Nginx\",\"Vue\",\"Bootstrap\"](선택)",
					},
					"auth": map[string]any{
						"type":        "array",
						"description": "발견한 인증 정보 목록, 각 항목에 type/username/password 등 필드 포함(선택, 추가이며 덮어쓰지 않음)",
						"items":       map[string]any{"type": "object"},
					},
					// service (other，非 HTTP)
					"service_name": str("서비스 이름, 예 ssh/mysql/redis(service 가 비HTTP 일 때 필수)"),
					"port":         intp("포트 번호(service 가 비HTTP 일 때 필수)"),
					// endpoint
					"method": str("HTTP 메서드: GET/POST/PUT/PATCH/DELETE 등(endpoint 필수)"),
					"params": map[string]any{
						"type":        "array",
						"description": "요청 파라미터 목록, 각 항목에 location(query/body/header/path)/name/value/type 포함(선택, 추가이며 덮어쓰지 않음)",
						"items":       map[string]any{"type": "object"},
					},
				}, "type"),
			},
		}, "assets"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("insert_assets 가 활성화되지 않음: AssetStore 가 초기화되지 않음"), nil
			}
			var a struct {
				Assets []assetInputItem `json:"assets"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			// task_id 由程序权威赋值(worker: SetTaskID)，不接受模型传入——避免模型漏传/错传
			// 导致资产未归任务或归错任务。无任务上下文的调用方(auto/pentest/chat)其 t.taskID=0。
			taskID := t.taskID

			type result struct {
				Index int    `json:"index"`
				ID    int64  `json:"id"`
				Type  string `json:"type"`
			}
			type errEntry struct {
				Index int    `json:"index"`
				Error string `json:"error"`
			}

			var results []result
			var errs []errEntry

			// 资产闸门规则一次性载入；读取失败则跳过判定（不阻断插入）。
			// 拦截规则 = 全局 ∪ 任务级 block；允许规则 = 任务级 allow。
			blockRules, _ := t.as.ListAssetInterceptRules()
			var allowRules []db.AssetInterceptRule
			if t.taskID > 0 {
				if tb, ta, err := t.as.TaskInterceptRulesSplit(t.taskID); err == nil {
					blockRules = append(blockRules, tb...)
					allowRules = ta
				}
			}

			for i, item := range a.Assets {
				// 资产闸门：先拦截后允许，被拒的资产禁止插入（跳过 Upsert 及后续副作用）。
				domains, ips, urls := assetInterceptCandidates(item)
				if d := db.EvaluateAssetGate(blockRules, allowRules, domains, ips, urls); !d.Allowed {
					errs = append(errs, errEntry{
						Index: i,
						Error: fmt.Sprintf("자산 %s %s, 삽입이 금지되었습니다", assetInputLabel(item), d.Reason),
					})
					continue
				}

				typ := strings.TrimSpace(item.Type)
				var id int64
				var err error

				switch typ {
				case "root_domain":
					id, err = t.as.UpsertRootDomain(db.UpsertRootDomainReq{
						Domain: item.Domain,
						ICP:    item.ICP,
						TaskID: taskID,
					})

				case "ip":
					id, err = t.as.UpsertIP(db.UpsertIPReq{
						IP:           item.IP,
						BoundDomains: item.BoundDomains,
						OpenPorts:    item.OpenPorts,
						TaskID:       taskID,
					})

				case "subdomain":
					id, err = t.as.UpsertSubdomain(db.UpsertSubdomainReq{
						Domain:      item.Domain,
						RecordType:  item.RecordType,
						RecordValue: item.RecordValue,
						ICP:         item.ICP,
						TaskID:      taskID,
					})

				case "app":
					id, err = t.as.UpsertApp(db.UpsertAppReq{
						Name:        item.AppName,
						BundleID:    item.BundleID,
						Category:    item.Category,
						Description: item.Description,
						ICP:         item.AppICP,
						CompanyID:   item.CompanyID,
						TaskID:      taskID,
					})

				case "service":
					// distinguish HTTP vs other by presence of url
					if item.URL != "" {
						// agent may send "ip" or "service_ip" for the enrichment IP; accept both
						svcIP := item.ServiceIP
						if svcIP == "" {
							svcIP = item.IP
						}
						id, err = t.as.UpsertHTTPService(db.UpsertHTTPServiceReq{
							URL:           item.URL,
							Technologies:  item.Technologies,
							StatusCode:    item.StatusCode,
							ContentLength: item.ContentLength,
							PageTitle:     item.PageTitle,
							FaviconMMH3:   item.FaviconMMH3,
							Auth:          item.Auth,
							IP:            svcIP,
							TaskID:        taskID,
						})
					} else {
						id, err = t.as.UpsertOtherService(db.UpsertOtherServiceReq{
							Domain:      item.Domain,
							IP:          item.IP,
							Port:        item.Port,
							ServiceName: item.ServiceName,
							Auth:        item.Auth,
							TaskID:      taskID,
						})
					}

				case "endpoint":
					id, err = t.as.UpsertEndpoint(db.UpsertEndpointReq{
						URL:    item.URL,
						Method: item.Method,
						Params: item.Params,
						IP:     item.ServiceIP,
						TaskID: taskID,
					})

				default:
					errs = append(errs, errEntry{Index: i, Error: "unknown type: " + typ})
					continue
				}

				if err != nil {
					errs = append(errs, errEntry{Index: i, Error: err.Error()})
					continue
				}
				results = append(results, result{Index: i, ID: id, Type: typ})
				t.writes.Assets++
				t.anchorOwner(id)
				if taskID > 0 {
					var sourceNodeID *int64
					if t.ownerNode > 0 {
						nodeID := t.ownerNode
						sourceNodeID = &nodeID
					}
					summary := "Agent 가 insert_assets 로 등록"
					if t.ownerNode > 0 {
						summary = fmt.Sprintf("Worker 의도 #%d 가 insert_assets 로 등록", t.ownerNode)
					}
					_ = t.as.SetTaskAssetSource(taskID, id, "agent", summary, sourceNodeID)
				}
				// 自动入测试范围(source='auto')：只对 worker 顶层显式插入的这一项，按其
				// 类型加保守范围；side-effect 派生的资产不经此处，故范围不盲目扩大。taskID=0 时无操作。
				// 与覆盖度开关无关：task_scope 是任务的范围边界(list/查询的过滤基准)，
				// 覆盖度开关只决定要不要把它当分母去算指标，不决定要不要累积范围本身。
				{
					svcIP := item.ServiceIP
					if svcIP == "" {
						svcIP = item.IP
					}
					_ = t.as.AddAutoScope(taskID, typ, item.Domain, item.URL, svcIP)
				}
			}

			return jsonResult(map[string]any{
				"results": results,
				"errors":  errs,
			})
		},
	)
}

// addCompanyScope writes to company_scope table and triggers asset attribution.
func (t *ToolSet) addCompanyScope() actool.CoreTool {
	return writeTool(
		"add_company_scope",
		"도메인/IP/CIDR/ICP 등록/기업 키워드를 특정 회사의 【자산 범위】에 추가합니다 — 도메인·네트워크·ICP 는 걸린 자산을 자동으로 귀속하고, 키워드는 Agent 에게 범위 힌트로만 제공됩니다.\n"+
			"회사명은 고유: company 가 없으면 새로 만들고, 있으면 재사용(범위만 병합).\n"+
			"scope 는 한 줄에 하나씩, 시스템이 자동 인식: 루트 도메인 / URL / 단일 IP / CIDR 대역 / ICP 등록 / 기업 키워드.\n"+
			"reason 에 귀속 근거(whois/인증서/ASN 등)를 반드시 설명하세요.\n"+
			"가드레일: 단독 TLD 와 지나치게 넓은 대역을 거부(IPv4 프리픽스는 /16-/32, IPv6 프리픽스는 /32-/128 이어야 함), 잘못된 줄은 건너뛰고 errors 로 반환됩니다.",
		obj(map[string]any{
			"company": str("회사명(없으면 새로 만들고 있으면 재사용; 이름 고유)"),
			"scope":   str("자산 범위, 한 줄에 하나씩: 도메인 / URL / IP / CIDR / ICP 등록 / 기업 키워드"),
			"reason":  str("귀속 근거(증거/출처), 반드시 입력"),
			"logo":    str("회사 아이콘 URL(선택; 회사 신규 생성 시에만 적용)"),
		}, "company", "scope"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.cs == nil {
				return actool.Errorf("add_company_scope 가 활성화되지 않음: CompanyStore 가 초기화되지 않음"), nil
			}
			var a struct {
				Company string `json:"company"`
				Scope   string `json:"scope"`
				Reason  string `json:"reason"`
				Logo    string `json:"logo"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if strings.TrimSpace(a.Company) == "" {
				return actool.Errorf("company 는 비울 수 없습니다"), nil
			}
			companyID, _, err := t.cs.UpsertCompany(a.Company, a.Logo)
			if err != nil {
				return actool.Errorf("회사 생성/조회 실패: " + err.Error()), nil
			}
			lines := splitLines(a.Scope)
			added, skipped, invalid, errMsgs := t.cs.AddScope(companyID, lines, a.Reason)
			out := map[string]any{
				"company_id": companyID,
				"added":      added,
				"skipped":    skipped,
				"invalid":    invalid,
			}
			if len(errMsgs) > 0 {
				out["errors"] = errMsgs
			}
			return jsonResult(out)
		},
	)
}

// addTaskScope lets the plan agent add test scope to THE CURRENT TASK — the coverage
// denominator and the task's authorization edge. Worker discoveries are auto-scoped
// (precise host) by insertAssets; this tool is for DELIBERATELY WIDENING: pull a whole
// root domain or whole company into scope, or add a specific subdomain / ip.
func (t *ToolSet) addTaskScope() actool.CoreTool {
	return writeTool(
		"add_task_scope",
		"테스트 범위를 【본 과제】에 추가합니다 — 이것이 본 과제의 승인 경계이자 자산 테스트 커버리지의 분모입니다.\n"+
			"kind 지원: company(회사 전체 자산) / root_domain(루트 도메인 전체, 모든 서브도메인 포함) / subdomain(정확한 단일 서브도메인) / ip / cidr / icp / keyword.\n"+
			"설명: worker 가 하나씩 마주치는 호스트는 시스템이 【자동】으로 범위에 추가합니다(정확한 서브도메인); 이 도구는 【능동적 확대】용 — 루트 도메인 전체/회사 전체를 포함하거나, 특정 서브도메인/IP 를 보충 지정합니다.\n"+
			"value: company 는 회사명이나 id 전달(회사가 이미 존재해야 함); root_domain/subdomain 은 도메인; ip/cidr 는 IP 나 대역; icp/keyword 는 등록번호나 기업 키워드 전달.\n"+
			"reason 에 근거를 반드시 설명하세요(감사 가능). 여러 개는 entries 배열 사용.",
		obj(map[string]any{
			"entries": map[string]any{"type": "array", "description": "배치: [{kind, value}]. kind∈company/root_domain/subdomain/ip/cidr/icp/keyword.", "items": map[string]any{"type": "object"}},
			"kind":    str("[단건] company / root_domain / subdomain / ip / cidr / icp / keyword"),
			"value":   str("[단건] 회사명이나 id / 도메인 / IP / CIDR / ICP / 키워드"),
			"reason":  str("추가 근거(감사용), 반드시 입력"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("add_task_scope 가 활성화되지 않음: AssetStore 가 초기화되지 않음"), nil
			}
			if t.taskID <= 0 {
				return actool.Errorf("add_task_scope 는 과제 컨텍스트가 필요(현재 task 없음)"), nil
			}
			type scopeEntry struct {
				Kind  string `json:"kind"`
				Value string `json:"value"`
			}
			var a struct {
				Entries    []scopeEntry `json:"entries"`
				scopeEntry              // 单条模式
				Reason     string       `json:"reason"`
			}
			_ = json.Unmarshal(in, &a)
			items := a.Entries
			if len(items) == 0 {
				items = []scopeEntry{a.scopeEntry}
			}
			var added []map[string]any
			errs := map[string]string{}
			for i, e := range items {
				ts, err := t.as.AddAgentScope(t.taskID, strings.TrimSpace(e.Kind), e.Value, a.Reason, "agent")
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				added = append(added, map[string]any{"kind": ts.Kind, "domain": ts.Domain, "net": ts.Net, "value": ts.Value, "company_id": ts.CompanyID})
			}
			out := map[string]any{"added": added}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		},
	)
}

// listUntestedAssets lets the plan agent pull the current + directly inherited
// scope's not-yet-tested assets on demand (filter by type, paginated).
func (t *ToolSet) listUntestedAssets() actool.CoreTool {
	return readTool(
		"list_untested_assets",
		"【본 과제와 직접 연관 과제】 범위 내에서 아직 사실 앵커로 덮이지 않은 자산을 조회합니다(연관 범위는 읽기 전용이며, 보충 테스트 여부를 스스로 판단하도록 제공할 뿐 결정을 대신하지 않음).\n"+
			"선택적으로 자산 유형 필터: root_domain/subdomain/service/app/endpoint/ip.\n"+
			"페이지네이션: page 는 1 부터, page_size 기본 10. {assets:[{id,type,label}], total, page, page_size} 반환. 과제 컨텍스트에서만 사용 가능.",
		obj(map[string]any{
			"type":      str("자산 유형 필터(선택): root_domain/subdomain/service/app/endpoint/ip"),
			"page":      intp("페이지 번호, 1 부터(기본 1)"),
			"page_size": intp("페이지당 개수(기본 10)"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("list_untested_assets 가 활성화되지 않음: AssetStore 가 초기화되지 않음"), nil
			}
			if t.taskID <= 0 || t.ts == nil {
				return actool.Errorf("list_untested_assets 는 과제 컨텍스트가 필요"), nil
			}
			var a struct {
				Type     string `json:"type"`
				Page     int    `json:"page"`
				PageSize int    `json:"page_size"`
			}
			_ = json.Unmarshal(in, &a)
			if a.Page <= 0 {
				a.Page = 1
			}
			if a.PageSize <= 0 {
				a.PageSize = 10
			}
			offset := (a.Page - 1) * a.PageSize
			assets, total, err := t.as.ListUntestedAssetsWithSources(t.taskID, strings.TrimSpace(a.Type), a.PageSize, offset)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{
				"assets": assets, "total": total, "page": a.Page, "page_size": a.PageSize,
			})
		},
	)
}

// listAssets lets an agent query the asset table.
func (t *ToolSet) listAssets() actool.CoreTool {
	return readTool(
		"list_assets",
		"자산 저장소 조회: DSL 표현식 검색, 또는 id/ids 로 직접 가져오기; 페이지네이션 지원. 【본 과제와 직접 연관 과제】 테스트 범위 내 자산만 반환.\n"+
			"DSL: field=value 부분일치(ILIKE) | field==value 정확 | field!=value 제외 | 숫자 필드는 > >= < <= 지원 | 단독 단어=전문 부분일치; AND/OR 조합(AND 우선순위 높음), 괄호로 그룹화 가능. 자산 유형은 별도 type 파라미터를 쓰고 DSL 에 넣지 않습니다.\n"+
			"id/ids 를 전달하지 않으면 dsl 은 비어 있을 수 없습니다(조건 없는 전체 조회 불가).\n"+
			"사용 가능 필드: domain(루트/서브/서비스 도메인), root_domain, ip, url, page_title, icp, service_name, app_name, method(예 GET/POST), service_type(http|other), record_type(예 A/CNAME), technology(배열, = 부분일치 == 정확), port/status_code/company_id(정수).\n"+
			"예: status_code>=400 AND technology=shiro ; (port==80 OR port==443) AND technology=nginx",
		obj(map[string]any{
			"dsl":    str(`DSL 조회 표현식(문법/필드는 도구 설명 참고). id/ids 를 전달하지 않으면 비울 수 없음.`),
			"type":   str("자산 유형 필터: root_domain|ip|subdomain|app|service|endpoint(별도 필드, dsl 과 함께 쓸 수 있음; type 단독으로는 조회 불가, 여전히 dsl 필요)"),
			"id":     intp("단일 자산 id 로 직접 가져오기(선택, dsl/type 와 상호 배타)"),
			"ids":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "여러 자산 id 로 직접 가져오기(선택, dsl/type 와 상호 배타)"},
			"limit":  intp("반환 상한, 기본 10(선택)"),
			"offset": intp("페이지 오프셋, 기본 0(선택)"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("list_assets 가 활성화되지 않음: AssetStore 가 초기화되지 않음"), nil
			}
			var a struct {
				DSL    string  `json:"dsl"`
				Type   string  `json:"type"`
				ID     int64   `json:"id"`
				IDs    []int64 `json:"ids"`
				Limit  int     `json:"limit"`
				Offset int     `json:"offset"`
			}
			_ = json.Unmarshal(in, &a)
			if a.Limit <= 0 {
				a.Limit = 10
			}

			var assets []*db.Asset
			var err error
			switch {
			case a.ID > 0:
				assets, err = t.as.GetByIDsInScope(t.taskID, []int64{a.ID})
			case len(a.IDs) > 0:
				assets, err = t.as.GetByIDsInScope(t.taskID, a.IDs)
			case a.DSL != "":
				assets, err = t.as.QueryDSLInScope(a.DSL, a.Type, t.taskID, a.Limit, a.Offset)
			default:
				return actool.Errorf("id/ids 를 전달하지 않으면 dsl 은 비울 수 없습니다: 조건 없이 전체 자산을 조회할 수 없으니 조회 조건을 제공하세요"), nil
			}
			if err != nil {
				return actool.Errorf("DSL 오류: " + err.Error()), nil
			}
			return jsonResult(map[string]any{
				"count":  len(assets),
				"assets": assets,
			})
		},
	)
}

// listCompanies lets an agent enumerate companies (企业) with their scope + asset count.
func (t *ToolSet) listCompanies() actool.CoreTool {
	return readTool(
		"list_companies",
		"자산 저장소의 【기업/회사】와 그 자산 범위(scope) 및 귀속된 자산 수를 나열합니다. 어떤 회사가 있는지 확인하고,"+
			"company_id 를 얻는 데 사용합니다(insert_assets 로 app 을 연관하거나 list_assets 를 company_id 로 필터할 때 사용)."+
			"선택 search 로 회사명 부분일치 필터(대소문자 무시), 비우면 전체 반환.",
		obj(map[string]any{
			"search": str("회사명 부분일치 필터(선택, 대소문자 무시); 비우면 전체 반환"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.cs == nil {
				return actool.Errorf("list_companies 가 활성화되지 않음: CompanyStore 가 초기화되지 않음"), nil
			}
			var a struct {
				Search string `json:"search"`
			}
			_ = json.Unmarshal(in, &a)
			cos, err := t.cs.ListCompanies()
			if err != nil {
				return actool.Errorf("회사 조회 실패: " + err.Error()), nil
			}
			q := strings.ToLower(strings.TrimSpace(a.Search))
			type companyOut struct {
				ID         int64    `json:"id"`
				Name       string   `json:"name"`
				AssetCount int      `json:"asset_count"`
				Scope      []string `json:"scope"`
			}
			out := make([]companyOut, 0, len(cos))
			for _, c := range cos {
				if q != "" && !strings.Contains(strings.ToLower(c.Name), q) {
					continue
				}
				scope := make([]string, 0, len(c.Scope))
				for _, r := range c.Scope {
					scope = append(scope, r.Raw)
				}
				out = append(out, companyOut{ID: c.ID, Name: c.Name, AssetCount: c.AssetCount, Scope: scope})
			}
			return jsonResult(map[string]any{"count": len(out), "companies": out})
		},
	)
}

// splitLines splits a multi-line string into non-empty trimmed lines.
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// WorkerTools returns the tool set for a work agent.
func (t *ToolSet) WorkerTools() []actool.CoreTool {
	return []actool.CoreTool{
		// list_findings 保留：报漏洞前先查本任务已确认漏洞，避免重复上报同一漏洞。
		t.listFindings(),
		t.addFinding(), t.recordFact(),
		// asset management (handlers guard nil store internally)。
		// add_company_scope 不给 worker：定义企业资产范围属规划/主控/Auto 的职责，worker 只执行探索。
		t.insertAssets(), t.listAssets(),
		// 跨 work 回看：worker 也可复用其他 work 的观察，避免重复劳动。
		// search_all_worker_traces：不必先知道 intent_id，按关键字全局捞命中步骤；
		// get_worker_trace：锁定某条 work 后列步骤/就地搜/取完整内容。
		t.searchAllWorkerTraces(), t.getWorkerTrace(),
		// node_detail：worker 拿到 intent_id/节点 id 后可查该节点完整详情（配合上面的回看）。
		t.nodeDetail(),
		// 以下工具仍【不给】worker，只留给 planner/main（读上下文、跨 work 复盘是规划职责，
		// worker 只做单条意图的执行与写回）：list_facts / list_companies / list_worker_traces。
	}
}

// MainAgentTools returns the human-interface tool set.
func (t *ToolSet) MainAgentTools() []actool.CoreTool {
	return []actool.CoreTool{
		t.graphOverview(), t.listFindings(), t.listFacts(), t.nodeDetail(),
		t.expandDigest(), // cold-digest §6.1
		t.getWorkerOutput(), t.getWorkerTrace(), t.searchAllWorkerTraces(), t.addHint(), t.addIntent(),
		// steer_work：人可对某条正在运行的意图(work)实时注入纠偏指令（不打断、不丢进展）。
		t.steerWorkTool(),
		// set_goals：人可在运行时给本任务补一个新的最终目标（规划者据此重判是否达成）。
		t.setGoals(),
		// set_constraints：人可在运行时给本任务补/改操作约束（allow/deny），约束 planner/worker 的探索边界。
		t.setConstraints(),
		// asset management (handlers guard nil store internally)
		t.insertAssets(), t.addCompanyScope(), t.listAssets(),
		t.addFinding(), t.recordFact(),
		t.addTaskScope(),
		// list_untested_assets：按需查本任务范围内未测资产(类型+分页)，自行决定补测。
		t.listUntestedAssets(),
	}
}

// AllDomainTools returns the union of all domain tools across all agent types,
// deduped by name (mainagent order wins). Used by the server to build a registry
// for injecting domain tools into agents (Auto, custom) that don't own a per-task
// ToolSet. The caller provides real stores; tools are callable at taskID=0 scope.
func (t *ToolSet) AllDomainTools() []actool.CoreTool {
	seen := map[string]bool{}
	var out []actool.CoreTool
	all := append(append(t.MainAgentTools(), t.PlannerTools()...), t.WorkerTools()...)
	for _, tool := range all {
		if !seen[tool.Name()] {
			seen[tool.Name()] = true
			out = append(out, tool)
		}
	}
	return out
}
