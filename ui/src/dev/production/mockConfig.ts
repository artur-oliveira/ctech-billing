// Production stand-in, aliased in by next.config.ts. Types are erased at build
// time, so re-exporting them costs nothing and keeps call sites compiling.
export type {MockScenario} from "@/lib/mockConfig"

export const USE_MOCK = false as const
export const MOCK_CUSTOMER = {customer_id: "", name: "", email: ""}
export const MOCK_SCENARIOS: { id: string; label: string }[] = []
export const DEFAULT_SCENARIO = "em_dia"
