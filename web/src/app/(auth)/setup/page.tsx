"use client";

import { useEffect, useState } from "react";

import { useRouter } from "next/navigation";

import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";

export default function SetupPage() {
  const router = useRouter();
  const t = useTranslations("auth");
  const [setupToken, setSetupToken] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(true);
  // 초기화 상태를 확인하지 못했을 때 "미초기화" 로 단정하면 안 된다 — 이미 비밀번호가
  // 설정된 인스턴스에도 초기화 폼을 내주게 되고, 사용자가 그대로 채우면 원래 비밀번호가
  // 덮인다. 백엔드도 읽기 실패를 503 으로 돌려주므로, 여기서는 초기화 입구를 닫는다.
  const [unavailable, setUnavailable] = useState(false);

  useEffect(() => {
    api
      .authStatus()
      .then(({ initialized }) => {
        if (initialized) router.replace("/login");
      })
      .catch((err) => {
        setError(err instanceof Error ? err.message : t("setup.errorBackend"));
        setUnavailable(true);
      })
      .finally(() => setChecking(false));
  }, [router, t]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!setupToken.trim()) {
      setError(t("setup.errorTokenEmpty"));
      return;
    }
    if (password !== confirm) {
      setError(t("setup.errorMismatch"));
      return;
    }
    if (password.length < 8) {
      setError(t("setup.errorTooShort"));
      return;
    }
    setLoading(true);
    setError("");
    try {
      const { token } = await api.initPassword(password, setupToken.trim());
      auth.setToken(token);
      router.replace("/function/tasks");
    } catch (err) {
      setError(err instanceof Error ? err.message : t("setup.errorInit"));
    } finally {
      setLoading(false);
    }
  }

  if (checking) return null;

  return (
    <div className="flex h-dvh">
      {/* Left panel */}
      <div className="hidden flex-col items-center justify-center bg-primary p-12 text-center lg:flex lg:w-1/3">
        <div className="relative flex items-center justify-center">
          <div className="absolute size-80 rounded-full border border-primary-foreground/10" />
          <div className="absolute size-60 rounded-full border border-primary-foreground/15" />
          <div className="absolute size-40 rounded-full border border-primary-foreground/20" />
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/logo.png" alt="ARTEX" width={160} height={160} className="relative brightness-0 invert" />
        </div>
      </div>

      {/* Right panel */}
      <div className="flex w-full items-center justify-center bg-background p-8 lg:w-2/3">
        <div className="w-full max-w-md space-y-10 py-24 lg:py-32">
          <div className="space-y-4 text-center">
            <h2 className="text-2xl font-medium tracking-tight">
              {unavailable ? t("setup.unavailableTitle") : t("setup.title")}
            </h2>
            <p className="mx-auto max-w-xl text-muted-foreground">
              {unavailable ? t("setup.unavailableDesc") : t("setup.desc")}
            </p>
          </div>
          {unavailable ? (
            <div className="flex flex-col gap-4">
              {error && <p className="text-center text-sm text-destructive">{error}</p>}
              <Button type="button" className="w-full" onClick={() => window.location.reload()}>
                {t("setup.retry")}
              </Button>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="flex flex-col gap-4">
              <div className="space-y-1.5">
                <Label htmlFor="setup-token">{t("setup.setupToken")}</Label>
                <Input
                  id="setup-token"
                  type="text"
                  value={setupToken}
                  onChange={(e) => setSetupToken(e.target.value)}
                  placeholder={t("setup.setupTokenPlaceholder")}
                  autoFocus
                  autoComplete="off"
                  spellCheck={false}
                />
                <p className="text-xs text-muted-foreground">{t("setup.setupTokenHelp")}</p>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="password">{t("setup.newPassword")}</Label>
                <Input
                  id="password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder={t("setup.newPasswordPlaceholder")}
                  autoComplete="new-password"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="confirm">{t("setup.confirmPassword")}</Label>
                <Input
                  id="confirm"
                  type="password"
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  placeholder={t("setup.confirmPlaceholder")}
                  autoComplete="new-password"
                />
              </div>
              {error && <p className="text-sm text-destructive">{error}</p>}
              <Button
                type="submit"
                className="w-full"
                disabled={loading || !setupToken.trim() || !password || !confirm}
              >
                {loading ? t("setup.submitting") : t("setup.submit")}
              </Button>
            </form>
          )}
        </div>
      </div>
    </div>
  );
}
