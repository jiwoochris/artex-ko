// 지원 locale 과 기본값. artex-ko 는 한국어를 기본으로 하고, 영어·중국어·스페인어를
// 더해 네 언어를 사용자 설정으로 전환할 수 있다. 중국어("zh")는 상류 원문 대조용으로도 함께 둔다.
export const LOCALES = ["en", "ko", "zh", "es"] as const;
export type Locale = (typeof LOCALES)[number];

// 기본 표시 언어는 한국어다. 영어·중국어·스페인어는 설정으로 전환하는 언어다. 빌드 시
// NEXT_PUBLIC_LOCALE 로 덮어쓸 수 있고, 실행 중에는 사용자가 설정 화면에서 고른 값(쿠키·localStorage)이 최종 결정권을 가진다.
export const DEFAULT_LOCALE: Locale = "ko";

// 언어 선택기에 보여 주는 각 locale 의 자국어 표기. 어떤 언어로 보고 있든 자기 언어
// 이름은 그 언어로 읽혀야 하므로 번역 대상이 아니라 고정 표기로 둔다.
export const LOCALE_LABELS: Record<Locale, string> = {
  en: "English",
  ko: "한국어",
  zh: "中文",
  es: "Español",
};

// 사용자가 고른 표시 언어를 저장하는 키. 쿠키는 정적 내보내기 중에도 부트 스크립트가
// <html lang> 을 맞추는 데 쓰고, localStorage 는 같은 탭 즉시 반영에 쓴다.
export const LOCALE_COOKIE = "artex_locale";
export const LOCALE_STORAGE_KEY = "artex_locale";

// 임의 문자열이 지원 locale 인지 좁혀 주는 가드.
export function isLocale(raw: unknown): raw is Locale {
  return typeof raw === "string" && (LOCALES as readonly string[]).includes(raw);
}

// 활성 locale 을 빌드 시점에 결정한다. 정적 내보내기(next.config 의 output: "export")와
// 호환되어야 하므로 cookies()·headers() 같은 동적 API 를 쓰지 않고 환경변수만 읽는다.
// NEXT_PUBLIC_LOCALE 이 비었거나 지원 목록 밖이면 기본값으로 떨어진다. 이 값은 서버
// 프리렌더의 초기 HTML 에만 쓰이고, 실제 사용자 표시 언어는 클라이언트에서 쿠키로 덮는다.
export function resolveLocale(): Locale {
  const raw = process.env.NEXT_PUBLIC_LOCALE;
  return isLocale(raw) ? raw : DEFAULT_LOCALE;
}
