"use client";

import * as React from "react";

import { setClientCookie } from "@/lib/cookie.client";
import { getLocalStorageValue, setLocalStorageValue } from "@/lib/local-storage.client";

import { DEFAULT_LOCALE, isLocale, LOCALE_COOKIE, LOCALE_STORAGE_KEY, type Locale, resolveLocale } from "./config";

// 사용자가 고른 표시 언어는 순수 프런트엔드 환경설정이라 서버에 저장하지 않고 쿠키와
// localStorage 에만 둔다. 쿠키는 정적 내보내기 중에도 <html lang> 부트 스크립트가 읽을
// 수 있고, localStorage 는 같은 탭 즉시 반영에 쓴다. 계정과 동기화되지 않으므로 브라우저를
// 바꾸거나 사이트 데이터를 지우면 기본 언어로 돌아간다.

function parseLocale(raw: string | null): Locale {
  return isLocale(raw) ? raw : resolveLocale();
}

// 같은 탭 안의 구독자 집합. storage 이벤트는 "다른" 탭에서만 발생하므로, 현재 탭에서
// 언어를 바꾼 뒤에는 emit 으로 같은 탭의 구독자에게 알려야 즉시 다시 렌더링된다.
const listeners = new Set<() => void>();

function subscribe(listener: () => void) {
  listeners.add(listener);
  window.addEventListener("storage", listener);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", listener);
  };
}

function emit() {
  for (const listener of listeners) listener();
}

function getSnapshot(): Locale {
  return parseLocale(getLocalStorageValue(LOCALE_STORAGE_KEY));
}

// 서버에는 localStorage 가 없으므로 먼저 빌드 기본 locale 을 렌더하고, hydrate 후
// getSnapshot 이 사용자가 고른 값으로 바로잡는다.
function getServerSnapshot(): Locale {
  return DEFAULT_LOCALE;
}

// 활성 표시 언어를 구독하는 훅. 언어가 바뀌면 이 훅을 쓰는 컴포넌트가 다시 렌더링된다.
export function useActiveLocale(): Locale {
  return React.useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

// 표시 언어를 바꾼다. 쿠키·localStorage 에 쓰고 같은 탭 구독자에게 알린다. 1년간 유지해
// 다음 방문에도 선택이 남게 한다.
export function setActiveLocale(locale: Locale) {
  if (!isLocale(locale)) return;
  setLocalStorageValue(LOCALE_STORAGE_KEY, locale);
  setClientCookie(LOCALE_COOKIE, locale, 365);
  emit();
}
