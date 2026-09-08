/********************************************************************
 * @author:      Kaven
 * @email:       kaven@wuwenkai.com
 * @website:     http://blog.kaven.xyz
 * @file:        [kaven-image] /eslint.config.js
 * @create:      2022-08-22 16:44:03.172
 * @modify:      2026-06-25 10:44:23.474
 * @version:     0.0.2
 * @times:       17
 * @lines:       154
 * @copyright:   Copyright © 2022-2026 Kaven. All Rights Reserved.
 * @description: [description]
 * @license:     [license]
 ********************************************************************/

import js from "@eslint/js";
import pluginQuasar from "@quasar/app-vite/eslint";
import stylistic from "@stylistic/eslint-plugin";
import { defineConfigWithVueTs, vueTsConfigs } from "@vue/eslint-config-typescript";
import * as config from "@wenkai.wu/eslint-config";
import pluginVue from "eslint-plugin-vue";

import { dirname } from "node:path";
import { fileURLToPath } from "node:url";

const globals = config.globals;

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

export default defineConfigWithVueTs(
    {
        /**
         * Ignore the following files.
         * Please note that pluginQuasar.configs.recommended() already ignores
         * the "node_modules" folder for you (and all other Quasar project
         * relevant folders and files).
         *
         * ESLint requires "ignores" key to be the only one in this object
         */
        ignores: ["src/share/**"],
    },

    pluginQuasar.configs.recommended(),
    js.configs.recommended,

    /**
 * https://eslint.vuejs.org
 *
 * pluginVue.configs.base
 *   -> Settings and rules to enable correct ESLint parsing.
 * pluginVue.configs[ 'flat/essential']
 *   -> base, plus rules to prevent errors or unintended behavior.
 * pluginVue.configs["flat/strongly-recommended"]
 *   -> Above, plus rules to considerably improve code readability and/or dev experience.
 * pluginVue.configs["flat/recommended"]
 *   -> Above, plus rules to enforce subjective community defaults to ensure consistency.
 */
    pluginVue.configs["flat/essential"],

    {
        files: ["**/*.ts", "**/*.vue"],
        rules: {
            "@typescript-eslint/consistent-type-imports": [
                "error",
                { prefer: "type-imports" },
            ],
        },
    },
    // https://github.com/vuejs/eslint-config-typescript
    vueTsConfigs.recommendedTypeChecked,

    {
        languageOptions: {
            ecmaVersion: "latest",
            sourceType: "module",

            globals: {
                ...globals.browser,
                ...globals.node, // SSR, Electron, config files
                process: "readonly", // process.env.*
                ga: "readonly", // Google Analytics
                cordova: "readonly",
                Capacitor: "readonly",
                chrome: "readonly", // BEX related
                browser: "readonly", // BEX related
            },
        },

        // add your custom rules here
        rules: {
            "prefer-promise-reject-errors": "off",

            // allow debugger during development only
            "no-debugger": process.env.NODE_ENV === "production" ? "error" : "off",
        },
    },

    {
        files: ["src-pwa/custom-service-worker.ts"],
        languageOptions: {
            globals: {
                ...globals.serviceworker,
            },
        },
    },

    {
        languageOptions:
        {
            parserOptions: {
                tsconfigRootDir: __dirname,
            },
        },
        plugins: {
            "@stylistic": stylistic,
        },
        rules: {
            ...config.rules,

            "@stylistic/indent": ["error", 4, { SwitchCase: 1 }],
            "@stylistic/quotes": ["error", "double"],
            "@stylistic/semi": ["error", "always"],
            "@stylistic/no-unused-vars": "off",
            "no-var": "error",
            "prefer-const": "error",
            "@stylistic/space-before-function-paren": ["error", {
                anonymous: "never",
                named: "never",
                asyncArrow: "always",
            }],
            "@stylistic/comma-dangle": ["error", "always-multiline"],
            "@stylistic/quote-props": ["error", "as-needed"],
            "no-constant-condition": ["error", { checkLoops: false }],
            "no-console": "off",



            "no-redeclare": "off",
            "@typescript-eslint/no-redeclare": ["error"],

            "prefer-promise-reject-errors": "off",
            "@typescript-eslint/prefer-promise-reject-errors": ["error", {
                allowThrowingAny: true,
                allowThrowingUnknown: true,
            }],

            "@typescript-eslint/no-unused-vars": "off",
            "@typescript-eslint/no-empty-object-type": "off",
            "@typescript-eslint/no-unsafe-enum-comparison": "off",
        },
    },
);
