/********************************************************************
 * @author:      Kaven
 * @email:       kaven@wuwenkai.com
 * @website:     http://blog.kaven.xyz
 * @file:        [kaven-image] /src/boot/mixin.ts
 * @create:      2022-08-25 18:03:10.425
 * @modify:      2025-09-09 15:32:19.963
 * @version:     0.0.2
 * @times:       6
 * @lines:       68
 * @copyright:   Copyright © 2022-2025 Kaven. All Rights Reserved.
 * @description: [description]
 * @license:     [license]
 ********************************************************************/

import { defineBoot } from "@quasar/app-vite";
import { toRaw } from "vue";

declare module "vue" {
    interface ComponentCustomProperties {
        Log: typeof console.log;
        Warn: typeof console.warn;
        Error: typeof console.error;
        Validate: (...names: string[]) => boolean;
        ResetValidation: (...names: string[]) => void;
    }
}

export default defineBoot(({ app }) => {
    app.mixin({
        methods: {
            Log(...data: never[]) {

                console.log(...data.map(toRaw));
            },
            Warn(...data: never[]) {

                console.warn(...data.map(toRaw));
            },
            Error(...data: never[]) {

                console.error(...data.map(toRaw));
            },
            Validate(this: { $refs: Record<string, unknown> }, ...names: string[]) {
                let pass = true;
                for (const name of names) {
                    const field = this.$refs[name] as {
                        validate: () => void;
                        hasError?: boolean;
                    } | undefined;
                    field?.validate();
                    if (field?.hasError) {
                        pass = false;
                    }
                }
                return pass;
            },
            ResetValidation(this: { $refs: Record<string, unknown> }, ...names: string[]) {
                for (const name of names) {
                    const field = this.$refs[name] as {
                        resetValidation: () => void;
                    } | undefined;
                    field?.resetValidation();
                }
            },
        },
    });
});
