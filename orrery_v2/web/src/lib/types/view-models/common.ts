export interface PageStatusViewModel {
  title: string;
  status: "loading" | "empty" | "error" | "ready";
}

export type BadgeTone =
  | "neutral"
  | "success"
  | "warning"
  | "danger"
  | "info"
  | "source-agent"
  | "source-human"
  | "blue"
  | "teal"
  | "violet"
  | "amber"
  | "rose";
