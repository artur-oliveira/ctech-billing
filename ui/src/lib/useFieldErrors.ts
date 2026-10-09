"use client"

import {useCallback, useMemo, useState} from "react"

import {fieldErrorsOf, messageFor, statusOf} from "@/lib/api/client"

interface State {
  byField: Record<string, string>
  general?: string
}

const EMPTY: State = {byField: {}}

/** The control an API path belongs to: "items[0].quantity" belongs to "items" and to itself. */
function controlFor(path: string, known: readonly string[]): string | undefined {
  if (known.includes(path)) return path
  return known.find(k => path.startsWith(`${k}.`) || path.startsWith(`${k}[`))
}

/**
 * Per-field validation errors for a form that submits to the API.
 *
 * `known` lists the API field paths the form has a control for. On a 422 with
 * field errors, each one lands on its own control (`of(field)`); a path with
 * no control falls back to `general`, together with every non-validation
 * failure (network, 409, 5xx), which keep their single message.
 *
 *   const fe = useFieldErrors(["amount", "due_date"])
 *   useMutation({..., onError: fe.set})
 *   <Field error={fe.of("amount")}><Input {...fe.props("amount", "x-amount")} onChange={...; fe.clear("amount")}/></Field>
 *   {fe.general && <p role="alert">{fe.general}</p>}
 *
 * In a dialog whose other failures are toasts, pass `{keepOthers: false}` and
 * `onError: e => { if (!fe.set(e)) toast.error(messageFor(e)) }`: `general` then
 * only ever holds validation leftovers.
 *
 * Call `clear(field)` when the user edits a control and `reset()` before a
 * new submit.
 */
export function useFieldErrors(known: readonly string[] = [], opts: {keepOthers?: boolean} = {}) {
  const keepOthers = opts.keepOthers ?? true
  const [state, setState] = useState<State>(EMPTY)
  const knownKey = known.join("\u0000")

  /** Returns true when `error` was a validation failure (field errors), false for anything else. */
  const set = useCallback((error: unknown): boolean => {
    const list = statusOf(error) === 422 ? fieldErrorsOf(error) : []
    if (list.length === 0) {
      setState({byField: {}, general: keepOthers ? messageFor(error) : undefined})
      return false
    }
    const keys = knownKey === "" ? [] : knownKey.split("\u0000")
    const byField: Record<string, string> = {}
    const rest: string[] = []
    for (const {field, message} of list) {
      const control = controlFor(field, keys)
      if (!control) {
        if (!rest.includes(message)) rest.push(message)
      } else if (!(control in byField)) {
        byField[control] = message
      }
    }
    setState({byField, general: rest.length ? rest.join(" ") : undefined})
    return true
  }, [knownKey, keepOthers])

  const clear = useCallback((field: string) => {
    setState(s => {
      if (!(field in s.byField)) return s
      const byField = {...s.byField}
      delete byField[field]
      return {...s, byField}
    })
  }, [])

  const reset = useCallback(() => setState(EMPTY), [])

  return useMemo(() => ({
    set,
    clear,
    reset,
    /** The message for one control, or undefined. */
    of: (field: string): string | undefined => state.byField[field],
    /** aria-invalid and aria-describedby for the control `id` of `field` (Field renders `${id}-error`). */
    props: (field: string, id: string) => state.byField[field]
      ? {"aria-invalid": true as const, "aria-describedby": `${id}-error`}
      : {},
    /** Everything no control claimed, or a non-validation failure's message. */
    general: state.general,
  }), [state, set, clear, reset])
}
