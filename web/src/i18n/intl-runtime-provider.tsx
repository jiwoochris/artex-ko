"use client";

import type { ReactNode } from "react";
import * as React from "react";

import { NextIntlClientProvider } from "next-intl";

import enMessages from "../../messages/en.json";
import esMessages from "../../messages/es.json";
import koMessages from "../../messages/ko.json";
import zhMessages from "../../messages/zh.json";
import type { Locale } from "./config";
import { useActiveLocale } from "./locale-store";

// 네 언어 메시지를 모두 정적으로 번들해 둔다. 그래야 사용자가 설정 화면에서 언어를
// 바꿀 때 재빌드나 페이지 새로고침, 비동기 로딩 없이 그 자리에서 모든 문구가 선택한
// 언어로 다시 렌더링된다(정적 내보내기 output: "export" 와도 호환된다).
const MESSAGES: Record<Locale, Record<string, unknown>> = {
  en: enMessages as Record<string, unknown>,
  ko: koMessages as Record<string, unknown>,
  zh: zhMessages as Record<string, unknown>,
  es: esMessages as Record<string, unknown>,
};

// 실행 중 언어 전환을 담당하는 프로바이더. 루트 레이아웃의 NextIntlClientProvider 를
// 대신해, 사용자가 고른 활성 locale(쿠키·localStorage)로 messages 를 골라 공급한다.
export function IntlRuntimeProvider({ children }: Readonly<{ children: ReactNode }>) {
  const locale = useActiveLocale();

  // 언어가 바뀌면 <html lang> 도 맞춘다. 루트 레이아웃이 서버에서 심은 초기 lang 을
  // 클라이언트 선택값으로 덮어, 스크린 리더·브라우저 번역이 올바른 언어로 동작하게 한다.
  React.useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  return (
    <NextIntlClientProvider locale={locale} messages={MESSAGES[locale]}>
      {children}
    </NextIntlClientProvider>
  );
}
