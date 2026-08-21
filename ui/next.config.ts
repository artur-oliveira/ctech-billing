import type {NextConfig} from "next"

const isProduction = process.env.NODE_ENV === "production"

const nextConfig: NextConfig = {
  devIndicators: false,
  turbopack: {
    // This directory, and nothing above it. It was widened to the repository's
    // parent while @aoctech/ui was installed as a `file:` link to a sibling
    // checkout — Turbopack refuses to resolve a module outside its root. The
    // package comes from npm now, so the root is back where it belongs and a
    // build no longer depends on what happens to sit next to this repository.
    root: __dirname,

    // In a production build the mock is not merely disabled, it is not built.
    // `USE_MOCK` already guards at runtime, but a guard still ships the
    // fixtures into the bundle; aliasing the modules to empty stubs means the
    // whole of src/dev is absent from what a customer downloads.
    //
    // These stay relative to *this* directory even though `root` is elsewhere:
    // Turbopack reads alias targets against the project, and both an absolute
    // path and a root-relative one fail to match.
    ...(isProduction
      ? {
          resolveAlias: {
            "@/dev/mockRuntime": "./src/dev/production/mockRuntime.ts",
            "@/dev/MockControls": "./src/dev/production/MockControls.tsx",
            "@/lib/mockConfig": "./src/dev/production/mockConfig.ts",
          },
        }
      : {}),
  },
  experimental: {optimizePackageImports: ["lucide-react"]},
  // There is no image optimizer behind a static export — the edge serves
  // bytes. The two images this app has are the logo, already sized.
  images: {unoptimized: true},
  // The portal ships as static assets on Cloudflare Workers, like every other
  // CTech front end, and calls the API cross-origin. `rewrites()` is
  // unsupported under `export` and only ever runs in `next dev`, so the two
  // branches below are mutually exclusive by construction rather than by
  // remembering — and the dev rewrite is the only same-origin path left.
  ...(isProduction
    ? {output: "export" as const}
    : {
        allowedDevOrigins: ["127.0.0.1"],
        async rewrites() {
          return [
            {
              source: "/v1.0/:path*",
              destination: `${process.env.DEV_API_ORIGIN || "http://localhost:8004"}/v1.0/:path*`,
            },
          ]
        },
      }),
}

export default nextConfig
