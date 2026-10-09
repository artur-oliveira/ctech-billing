/**
 * A secondary link beside a heading ("Ver todas", "Ver relatórios"): body-size,
 * muted, underlined on hover. Under `touch:` it grows to a 44px target without
 * changing how it looks. Not for a link inside a sentence, which is exempt and
 * should stay inline.
 */
export const quietLink =
  "text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline touch:inline-flex touch:min-h-11 touch:items-center"
