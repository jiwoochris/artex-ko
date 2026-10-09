package config

// 기동 단계(DB 연결 이전)의 운영자 대면 메시지 번역. 이 시점에는 settings 테이블을 읽을 수
// 없으므로 런타임 설정(ActiveLanguage)이 아니라 구성된 기본 언어(Language(): ARTEX_LANG >
// 설정 파일 > ko)를 기준으로 치환한다. 조치에 필요한 식별자(ARTEX_PG_DSN·database·dsn·
// host/user/dbname 등)는 번역하지 않고 원문을 그대로 둔다. "ko" 는 키 자체이므로 저장하지
// 않고, 조회 실패·ko 일 때는 원문을 돌려준다.
var cfgCatalog = map[string]map[string]string{
	"환경 변수 ARTEX_PG_DSN": {
		"en": "environment variable ARTEX_PG_DSN",
		"zh": "环境变量 ARTEX_PG_DSN",
		"es": "variable de entorno ARTEX_PG_DSN",
	},
	"설정 파일 %s (database.dsn)": {
		"en": "config file %s (database.dsn)",
		"zh": "配置文件 %s (database.dsn)",
		"es": "archivo de configuración %s (database.dsn)",
	},
	"설정 파일 %s (database 필드)": {
		"en": "config file %s (database fields)",
		"zh": "配置文件 %s (database 字段)",
		"es": "archivo de configuración %s (campos database)",
	},
	"데이터베이스 설정을 찾을 수 없습니다: 환경 변수 ARTEX_PG_DSN 이 설정되어 있지 않고, 설정 파일 %s 에도 database (dsn 또는 host/user/dbname) 설정이 없습니다. 설정 파일을 만들거나 환경 변수를 설정한 뒤 다시 시도하세요": {
		"en": "database configuration not found: environment variable ARTEX_PG_DSN is not set, and config file %s has no database (dsn or host/user/dbname) configuration. Create a config file or set the environment variable, then try again",
		"zh": "找不到数据库配置：环境变量 ARTEX_PG_DSN 未设置，配置文件 %s 中也没有 database（dsn 或 host/user/dbname）配置。请创建配置文件或设置环境变量后重试",
		"es": "no se encontró la configuración de la base de datos: la variable de entorno ARTEX_PG_DSN no está definida y el archivo de configuración %s tampoco tiene configuración database (dsn o host/user/dbname). Cree un archivo de configuración o defina la variable de entorno e inténtelo de nuevo",
	},
}

// logCatalog holds operator-facing console log templates (startup + runtime
// diagnostics). Keyed by the Korean source template; format verbs are preserved in
// the same positional order so fmt.Printf/log.Printf arguments still line up.
var logCatalog = map[string]map[string]string{
	"[config] 설정 파일: %s": {
		"en": "[config] config file: %s", "zh": "[config] 配置文件: %s", "es": "[config] archivo de configuración: %s",
	},
	"[config] 설정 파일: %s (파일 없음 · 환경 변수 ARTEX_PG_DSN 만 사용)": {
		"en": "[config] config file: %s (not found · using only environment variable ARTEX_PG_DSN)",
		"zh": "[config] 配置文件: %s (文件不存在 · 仅使用环境变量 ARTEX_PG_DSN)",
		"es": "[config] archivo de configuración: %s (no encontrado · usando solo la variable de entorno ARTEX_PG_DSN)",
	},
	"[config] skill 디렉터리: %s": {
		"en": "[config] skill directory: %s", "zh": "[config] skill 目录: %s", "es": "[config] directorio de skills: %s",
	},
	"일시정지됨": {"en": "paused", "zh": "已暂停", "es": "pausada"},
	"재개됨":   {"en": "resumed", "zh": "已恢复", "es": "reanudada"},
	"[auth] JWT 키를 %s 에서 %s 로 이전(탐색 가능한 워크스페이스 밖으로 이동)": {
		"en": "[auth] moved JWT key from %s to %s (out of the discoverable workspace)",
		"zh": "[auth] 已将 JWT 密钥从 %s 迁移到 %s（移出可被发现的工作区）",
		"es": "[auth] clave JWT movida de %s a %s (fuera del espacio de trabajo detectable)",
	},
	"[auth] 새 JWT 키를 %s 에 기록": {
		"en": "[auth] wrote new JWT key to %s", "zh": "[auth] 已将新的 JWT 密钥写入 %s", "es": "[auth] nueva clave JWT escrita en %s",
	},
	"[auth] %s 가 %d자 미만이라 무시합니다. 설정 토큰 방식으로 초기화하세요": {
		"en": "[auth] ignoring %s because it is shorter than %d characters; initialise with the setup token instead",
		"zh": "[auth] %s 少于 %d 个字符，已忽略。请使用设置令牌方式初始化",
		"es": "[auth] se ignora %s porque tiene menos de %d caracteres; inicialice con el token de configuración",
	},
	"[auth] %s 해시 생성 실패: %v": {
		"en": "[auth] failed to hash %s: %v",
		"zh": "[auth] 生成 %s 哈希失败: %v",
		"es": "[auth] no se pudo generar el hash de %s: %v",
	},
	"[auth] %s 저장 실패: %v": {
		"en": "[auth] failed to store %s: %v",
		"zh": "[auth] 保存 %s 失败: %v",
		"es": "[auth] no se pudo guardar %s: %v",
	},
	"[auth] 환경 변수 %s 로 관리자 비밀번호를 초기화했습니다(사용자 이름 ARTEX)": {
		"en": "[auth] initialised the admin password from environment variable %s (username ARTEX)",
		"zh": "[auth] 已通过环境变量 %s 初始化管理员密码（用户名 ARTEX）",
		"es": "[auth] contraseña de administrador inicializada desde la variable de entorno %s (usuario ARTEX)",
	},
	"[auth] 관리자 비밀번호가 아직 설정되지 않았습니다. 설정 토큰은 환경 변수 %s 값입니다(로그에 출력하지 않음)": {
		"en": "[auth] the admin password has not been set yet. The setup token is the value of environment variable %s (not printed to the log)",
		"zh": "[auth] 管理员密码尚未设置。设置令牌为环境变量 %s 的值（不输出到日志）",
		"es": "[auth] aún no se ha establecido la contraseña de administrador. El token de configuración es el valor de la variable de entorno %s (no se muestra en el registro)",
	},
	"[auth] 설정 토큰 생성 실패: %v": {
		"en": "[auth] failed to generate the setup token: %v",
		"zh": "[auth] 生成设置令牌失败: %v",
		"es": "[auth] no se pudo generar el token de configuración: %v",
	},
	"[auth] 관리자 비밀번호가 아직 설정되지 않았습니다. 설정 토큰: %s": {
		"en": "[auth] the admin password has not been set yet. Setup token: %s",
		"zh": "[auth] 管理员密码尚未设置。设置令牌: %s",
		"es": "[auth] aún no se ha establecido la contraseña de administrador. Token de configuración: %s",
	},
	"[auth] 첫 화면(/setup)의 \"설정 토큰\" 칸에 위 값을 입력하거나 POST /api/auth/init 본문의 setup_token 으로 보내십시오. 이 토큰은 네트워크로 전달되지 않으며 이 콘솔 로그에서만 확인할 수 있습니다": {
		"en": "[auth] enter the value above in the \"Setup token\" field on the first screen (/setup), or send it as setup_token in the POST /api/auth/init body. This token is never sent over the network and is only visible in this console log",
		"zh": "[auth] 请在首个页面（/setup）的“设置令牌”栏中输入上述值，或将其作为 POST /api/auth/init 请求体中的 setup_token 发送。该令牌不会通过网络传输，只能在此控制台日志中查看",
		"es": "[auth] introduzca el valor anterior en el campo \"Token de configuración\" de la primera pantalla (/setup) o envíelo como setup_token en el cuerpo de POST /api/auth/init. Este token nunca se transmite por la red y solo puede verse en este registro de consola",
	},
	"[auth] 경고: 관리자 비밀번호가 아직 설정되지 않았는데 서버가 %q 에 바인딩되어 다른 호스트에서도 접속할 수 있습니다. 설정 토큰 없이는 초기화할 수 없지만, 초기 설정을 마칠 때까지 이 포트를 외부 네트워크에 노출하지 마십시오(기본값 127.0.0.1:8787)": {
		"en": "[auth] warning: the admin password has not been set yet and the server is bound to %q, so other hosts can connect. Initialisation still requires the setup token, but do not expose this port to external networks until initial setup is complete (default 127.0.0.1:8787)",
		"zh": "[auth] 警告：管理员密码尚未设置，但服务器绑定在 %q，其他主机也可以连接。虽然没有设置令牌无法初始化，但在完成初始设置之前请勿将此端口暴露到外部网络（默认 127.0.0.1:8787）",
		"es": "[auth] advertencia: aún no se ha establecido la contraseña de administrador y el servidor está enlazado a %q, por lo que otros hosts pueden conectarse. La inicialización sigue requiriendo el token de configuración, pero no exponga este puerto a redes externas hasta completar la configuración inicial (predeterminado 127.0.0.1:8787)",
	},
	"[auth] 초기 설정 거부: 설정 토큰 불일치 (client=%s)": {
		"en": "[auth] initial setup rejected: setup token mismatch (client=%s)",
		"zh": "[auth] 拒绝初始设置：设置令牌不匹配 (client=%s)",
		"es": "[auth] configuración inicial rechazada: el token de configuración no coincide (client=%s)",
	},
	"[auth] 관리자 비밀번호를 초기화했습니다 (client=%s)": {
		"en": "[auth] admin password initialised (client=%s)",
		"zh": "[auth] 已初始化管理员密码 (client=%s)",
		"es": "[auth] contraseña de administrador inicializada (client=%s)",
	},
	"[auth] 비밀번호 변경 거부: 현재 비밀번호 불일치 (client=%s)": {
		"en": "[auth] password change rejected: current password mismatch (client=%s)",
		"zh": "[auth] 拒绝修改密码：当前密码不匹配 (client=%s)",
		"es": "[auth] cambio de contraseña rechazado: la contraseña actual no coincide (client=%s)",
	},
	"[auth] 관리자 비밀번호를 변경했습니다. 기존 세션은 모두 무효화됩니다 (client=%s)": {
		"en": "[auth] admin password changed; all existing sessions are invalidated (client=%s)",
		"zh": "[auth] 已修改管理员密码，所有现有会话均已失效 (client=%s)",
		"es": "[auth] contraseña de administrador cambiada; todas las sesiones existentes quedan invalidadas (client=%s)",
	},
	"[auth] 로그인 실패 (client=%s)": {
		"en": "[auth] login failed (client=%s)",
		"zh": "[auth] 登录失败 (client=%s)",
		"es": "[auth] inicio de sesión fallido (client=%s)",
	},
	"[notify] 백그라운드 알림 전송 루프가 %s 로 꺼졌습니다(테스트 전용) — 취약점 IM 알림이 전송되지 않습니다": {
		"en": "[notify] background notification loop disabled via %s (test only) — vulnerability IM alerts will not be sent",
		"zh": "[notify] 后台通知发送循环已通过 %s 关闭（仅测试）— 不会发送漏洞 IM 通知",
		"es": "[notify] el bucle de notificaciones en segundo plano se desactivó mediante %s (solo pruebas) — no se enviarán alertas IM de vulnerabilidades",
	},
	"[mcp] %s 에서 도구 %d개를 발견해 캐시했습니다": {
		"en": "[mcp] from %s, discovered and cached %d tool(s)",
		"zh": "[mcp] 从 %s 发现并缓存了 %d 个工具",
		"es": "[mcp] desde %s, se descubrieron y almacenaron en caché %d herramienta(s)",
	},
	"[mcp] 시작 시 자동 발견: 목록을 읽지 못했습니다: %v": {
		"en": "[mcp] auto-discovery at startup: failed to read the list: %v",
		"zh": "[mcp] 启动时自动发现：读取列表失败：%v",
		"es": "[mcp] descubrimiento automático al inicio: no se pudo leer la lista: %v",
	},
	"[mcp] 시작 시 %s 자동 발견에 실패했습니다: %v": {
		"en": "[mcp] auto-discovery of %s at startup failed: %v",
		"zh": "[mcp] 启动时自动发现 %s 失败：%v",
		"es": "[mcp] falló el descubrimiento automático de %s al inicio: %v",
	},
	"[tools] orchestration/platform 도구 스키마를 코드 기본값으로 새로고침(1회성)": {
		"en": "[tools] refreshed orchestration/platform tool schemas to code defaults (one-time)",
		"zh": "[tools] 已将 orchestration/platform 工具 schema 刷新为代码默认值（一次性）",
		"es": "[tools] esquemas de herramientas orchestration/platform actualizados a los valores predeterminados del código (una sola vez)",
	},
	"[reporter] 「%s」 agent + finding 트리거 사전 구성": {
		"en": "[reporter] pre-configured the \"%s\" agent + finding trigger",
		"zh": "[reporter] 已预配置「%s」agent + finding 触发器",
		"es": "[reporter] agente \"%s\" + disparador de finding preconfigurados",
	},
	"[custom-tool] python 인터프리터 자동 감지: %s": {
		"en": "[custom-tool] auto-detected python interpreter: %s",
		"zh": "[custom-tool] 自动检测到 python 解释器：%s",
		"es": "[custom-tool] intérprete de python detectado automáticamente: %s",
	},
	"[pg] 데이터베이스 설정 출처: %s": {
		"en": "[pg] database configuration source: %s", "zh": "[pg] 数据库配置来源：%s", "es": "[pg] origen de la configuración de la base de datos: %s",
	},
	"[mcp] browser MCP 캡처 프록시 연결: %s (CA %s)": {
		"en": "[mcp] browser MCP capture proxy attached: %s (CA %s)",
		"zh": "[mcp] browser MCP 抓包代理已连接：%s (CA %s)",
		"es": "[mcp] proxy de captura de browser MCP conectado: %s (CA %s)",
	},
	"[mcp] browser MCP 캡처 프록시 설정 제거": {
		"en": "[mcp] browser MCP capture proxy configuration removed",
		"zh": "[mcp] 已移除 browser MCP 抓包代理配置",
		"es": "[mcp] configuración del proxy de captura de browser MCP eliminada",
	},
}

// trCfg translates a startup/operator message to the configured default language
// (Language()). Format templates are translated the same way: pass the Korean
// template through trCfg before fmt.Sprintf/fmt.Errorf.
func trCfg(ko string) string {
	if m, ok := cfgCatalog[ko]; ok {
		if v, ok2 := m[Language()]; ok2 && v != "" {
			return v
		}
	}
	return ko
}

// T translates an operator-facing console log template to the configured default
// language (Language()). Exposed for cmd and server log.Printf call sites. Format
// verbs are preserved in order, so wrap the template only: log.Printf(config.T(ko), args...).
func T(ko string) string {
	if m, ok := logCatalog[ko]; ok {
		if v, ok2 := m[Language()]; ok2 && v != "" {
			return v
		}
	}
	return ko
}
