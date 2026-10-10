import {LegacyFinanceRedirect} from "@/components/finance/LegacyFinanceRedirect"

/** Finanças moved to /finance (its own area); this keeps old links working. */
export default function Page() {
  return <LegacyFinanceRedirect/>
}
