"use client";

import {useSearchParams} from "next/navigation";
import {Suspense} from "react";

import {ClosureLinkPage} from "@/components/ClosureLinkPage";
import {confirmClosure} from "@/lib/api/closure";

function ConfirmFromLink() {
  const params = useSearchParams();
  return <ClosureLinkPage kind="confirm" requestID={params.get("request") ?? ""} token={params.get("token") ?? ""} action={confirmClosure}/>;
}

export default function ClosureConfirmPage() {
  return <Suspense fallback={null}><ConfirmFromLink/></Suspense>;
}
