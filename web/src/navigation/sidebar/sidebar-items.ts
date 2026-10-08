import {
  Activity,
  Ban,
  BellRing,
  Bot,
  Brain,
  Bug,
  ClipboardList,
  FolderOpen,
  FolderSync,
  LayoutDashboard,
  type LucideIcon,
  MessageSquare,
  Network,
  Plug,
  Radio,
  ScrollText,
  Settings2,
  ShieldAlert,
  Sparkles,
  Target,
  Terminal,
  Wrench,
} from "lucide-react";

export type NavBadge = "new" | "soon";

export interface NavSubItem {
  id: string;
  title: string;
  url: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

interface NavItemBase {
  id: string;
  title: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

export interface NavMainLinkItem extends NavItemBase {
  url: string;
  subItems?: never;
}

export interface NavMainParentItem extends NavItemBase {
  subItems: NavSubItem[];
}

export type NavMainItem = NavMainLinkItem | NavMainParentItem;

export interface NavGroup {
  id: number;
  label?: string;
  items: NavMainItem[];
}

export const sidebarItems: NavGroup[] = [
  {
    id: 1,
    label: "기능",
    items: [
      { id: "dashboard", title: "대시보드", url: "/dashboard", icon: LayoutDashboard },
      { id: "chat", title: "대화", url: "/chat", icon: MessageSquare },
      { id: "tasks", title: "작업", url: "/function/tasks", icon: Target },
      { id: "findings", title: "취약점", url: "/function/findings", icon: Bug },
      { id: "traffic", title: "트래픽", url: "/function/traffic", icon: Activity },
      { id: "commands", title: "도구 실행", url: "/function/commands", icon: Terminal },
      { id: "llm-records", title: "LLM 기록", url: "/function/llm-records", icon: Radio },
      { id: "assets", title: "자산", url: "/function/assets", icon: Network },
      { id: "sync", title: "자산 동기화", url: "/function/sync", icon: FolderSync },
      { id: "workspace", title: "작업 공간", url: "/function/workspace", icon: FolderOpen },
    ],
  },
  {
    id: 2,
    label: "시스템",
    items: [
      { id: "llm", title: "LLM", url: "/system/llm", icon: Brain },
      { id: "agents", title: "에이전트", url: "/system/agents", icon: Bot },
      { id: "mcp", title: "MCP", url: "/system/mcp", icon: Plug },
      { id: "skills", title: "스킬", url: "/system/skills", icon: Sparkles },
      { id: "tools", title: "도구", url: "/system/tools", icon: Wrench },
      { id: "notify", title: "알림 발송", url: "/system/notify", icon: BellRing },
      { id: "intercept", title: "가로채기 규칙", url: "/system/intercept", icon: ShieldAlert },
      { id: "asset-intercept", title: "자산 차단", url: "/system/intercept/assets", icon: Ban },
      { id: "approvals", title: "승인 기록", url: "/system/intercept/approvals", icon: ClipboardList },
      { id: "logs", title: "로그", url: "/system/logs", icon: ScrollText },
      { id: "settings", title: "시스템 설정", url: "/system/settings", icon: Settings2 },
    ],
  },
];
