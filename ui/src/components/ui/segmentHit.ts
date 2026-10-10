/**
 * The hit area of a segment billing draws itself (a tab list that is also a
 * segmented group, the mode switch): 44px tall, centred, exactly as wide as the
 * segment, so it never reaches into a neighbour. @aoctech/ui's `Segmented`
 * draws the same on its own items; its touch.css only sets their height
 * (`data-slot="segmented-item"`), so a hand-made segment needs this to keep a
 * finger's target (UX batch 5). Keep such a group on one line: wrapped rows
 * 2px apart would share their targets.
 */
export const segmentHit = "relative after:absolute after:inset-x-0 after:top-1/2 after:h-11 after:min-h-full after:-translate-y-1/2 after:content-['']"
