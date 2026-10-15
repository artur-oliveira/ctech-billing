"use client";

import {createContext, useContext} from "react";

import type {RealtimeStatus} from "./core";

export const RealtimeStatusContext = createContext<RealtimeStatus>("off");

/** Where the live connection stands. "off" without a provider or with the flag off. */
export function useRealtimeStatus(): RealtimeStatus {
  return useContext(RealtimeStatusContext);
}
