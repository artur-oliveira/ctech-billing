// Production stand-in, aliased in by next.config.ts. Nothing here is ever
// reached: USE_MOCK is false, so no call site runs. It exists so the dynamic
// imports still resolve and the fixtures stay out of the bundle.
import type {AxiosAdapter} from "axios"

export const mockAdapter: AxiosAdapter = () => {
  throw new Error("mock adapter is not available in production")
}
export const settleAfterSeconds = (): number | null => null
export const settleInvoice = (): void => undefined
export const currentScenario = () => "em_dia"
export const setScenario = (): void => undefined
