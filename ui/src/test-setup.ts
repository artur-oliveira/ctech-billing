import {configure} from "@testing-library/react"

import "@/lib/i18n"

// These are interaction tests (typing, opening popups) running in a few dozen
// jsdom workers at once. The defaults (5 s a test, 1 s a waitFor) are sized for
// an idle machine: under a full run the same test took 5.6 s and failed on the
// clock rather than on anything it asserts.
configure({asyncUtilTimeout: 4000})
