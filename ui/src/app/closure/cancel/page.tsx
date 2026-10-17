"use client";

import {useSearchParams} from "next/navigation";
import {Suspense} from "react";

import {ClosureLinkPage} from "@/components/ClosureLinkPage";
import {cancelClosure} from "@/lib/api/closure";

function CancelFromLink() {
  const params = useSearchParams();
  return <ClosureLinkPage kind="cancel" requestID={params.get("request") ?? ""} token={params.get("token") ?? ""} action={cancelClosure}/>;
}

export default function ClosureCancelPage() {
  return <Suspense fallback={null}><CancelFromLink/></Suspense>;
}
