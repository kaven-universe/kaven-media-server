import { defineBoot } from "@quasar/app-vite";
import { createI18n } from "vue-i18n";

import messages from "src/i18n";

export type SupportedLocale = "en-US" | "zh-CN";
export const localeStorageKey = "kaven-media-locale";

function initialLocale(): SupportedLocale {
    try {
        const stored = localStorage.getItem(localeStorageKey);
        if (stored === "en-US" || stored === "zh-CN") return stored;
    } catch {
        // Storage may be unavailable in privacy-restricted browsers.
    }
    return navigator.language.toLowerCase().startsWith("zh") ? "zh-CN" : "en-US";
}

export default defineBoot(({ app }) => {
    const i18n = createI18n({
        legacy: false,
        locale: initialLocale(),
        fallbackLocale: "en-US",
        globalInjection: true,
        messages,
    });

    // Set i18n instance on app
    app.use(i18n);
});
