import {defineConfig, globalIgnores} from 'eslint/config';
import nextVitals from 'eslint-config-next/core-web-vitals';
import nextTs from 'eslint-config-next/typescript';

export default defineConfig([
    ...nextVitals,
    ...nextTs,
    globalIgnores([
        // Default ignores of eslint-config-next:
        ".next/**",
        "out/**",
        "build/**",
        "next-env.d.ts",
    ]),
    {
        rules: {
            "consistent-return": 2,
            "no-else-return": 1,
            "semi": [1, "always"],
            "space-unary-ops": 2
        },
        settings: {
            react: {version: '19'},
        },
    }
]);
