<template>
    <q-layout view="lHh Lpr lFf">
        <q-header elevated>
            <q-toolbar>
                <q-toolbar-title>
                    Kaven Media Server
                </q-toolbar-title>

                <q-space />

                <q-btn-toggle v-model="tab" flat stretch toggle-color="yellow" :options="options" />

                <q-btn-dropdown class="q-ml-sm" flat icon="language" :label="locale === 'zh-CN' ? '中文' : 'English'" :aria-label="t('common.language')">
                    <q-list>
                        <q-item v-close-popup clickable :active="locale === 'en-US'" @click="setLocale('en-US')">
                            <q-item-section>{{ t("common.english") }}</q-item-section>
                        </q-item>
                        <q-item v-close-popup clickable :active="locale === 'zh-CN'" @click="setLocale('zh-CN')">
                            <q-item-section>{{ t("common.chinese") }}</q-item-section>
                        </q-item>
                    </q-list>
                </q-btn-dropdown>

                <q-btn
                    v-if="!adminSession.authenticated.value"
                    class="q-ml-sm"
                    flat
                    icon="login"
                    :label="t('nav.login')"
                    :loading="adminSession.authenticating.value || adminSession.checking.value"
                    :disable="adminSession.checking.value"
                    @click="openLogin"
                />
                <div v-else class="row items-center no-wrap q-ml-sm">
                    <q-chip color="white" text-color="primary" icon="person">
                        {{ t("common.admin") }}
                    </q-chip>
                    <q-btn
                        flat
                        icon="logout"
                        :label="t('nav.logout')"
                        :disable="adminSession.checking.value"
                        @click="logout"
                    />
                </div>
            </q-toolbar>
        </q-header>

        <q-page-container>
            <router-view />
        </q-page-container>

        <q-dialog v-model="loginOpen" persistent>
            <q-card class="login-card">
                <q-form @submit.prevent="login">
                    <q-card-section>
                        <div class="text-h6">{{ t("auth.title") }}</div>
                    </q-card-section>

                    <q-card-section class="q-pt-none q-gutter-md">
                        <q-input
                            v-model="loginUsername"
                            autofocus
                            outlined
                            :label="t('auth.username')"
                            name="username"
                            autocomplete="username"
                            :disable="adminSession.authenticating.value"
                        />
                        <q-input
                            v-model="loginPassword"
                            outlined
                            :label="t('auth.password')"
                            name="password"
                            type="password"
                            autocomplete="current-password"
                            :disable="adminSession.authenticating.value"
                        />
                        <q-checkbox
                            v-model="rememberLogin"
                            :label="t('auth.remember')"
                            :disable="adminSession.authenticating.value"
                        />
                    </q-card-section>

                    <q-card-actions align="right">
                        <q-btn v-close-popup flat :label="t('common.cancel')" :disable="adminSession.authenticating.value" />
                        <q-btn
                            color="primary"
                            :label="t('nav.login')"
                            type="submit"
                            :loading="adminSession.authenticating.value"
                            :disable="!loginUsername || !loginPassword"
                        />
                    </q-card-actions>
                </q-form>
            </q-card>
        </q-dialog>
    </q-layout>
</template>

<script setup lang="ts">
import { ConvertTo } from "kaven-basic";
import axios from "axios";
import { useQuasar } from "quasar";
import langEnUS from "quasar/lang/en-US";
import langZhCN from "quasar/lang/zh-CN";
import { localeStorageKey, type SupportedLocale } from "src/boot/i18n";
import { GetRouteByName, Logger, RouteName } from "src/common";
import { useAdminSessionStore } from "src/stores/admin-session";
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useI18n } from "vue-i18n";

const $q = useQuasar();
const route = useRoute();
const router = useRouter();
const adminSession = useAdminSessionStore();
const { t, locale } = useI18n();
const loginOpen = ref(false);
const loginUsername = ref("admin");
const loginPassword = ref("");
const rememberLogin = ref(false);

const tab = ref<RouteName>(
    ConvertTo(route.name?.toString() ?? RouteName.Upload),
);

const options = computed(() => Object.keys(RouteName)
    .filter(name => name === "Upload" || adminSession.authenticated.value)
    .map(name => ({
        label: t(`nav.${name.toLowerCase()}`),
        value: GetRouteByName(name),
    })));

function openLogin() {
    loginPassword.value = "";
    loginOpen.value = true;
}

function setLocale(value: SupportedLocale) {
    locale.value = value;
}

async function login() {
    try {
        await adminSession.login(loginUsername.value, loginPassword.value, rememberLogin.value);
        loginPassword.value = "";
        loginOpen.value = false;
        $q.notify({ type: "positive", message: t("auth.loginSuccess") });
    } catch (error) {
        const status = axios.isAxiosError(error) ? error.response?.status : undefined;
        let message = t("auth.loginFailed");
        if (status === 404) {
            message = t("auth.notConfigured");
        } else if (status === 429) {
            message = t("auth.tooManyAttempts");
        }
        $q.notify({
            type: "negative",
            message,
        });
    }
}

async function logout() {
    try {
        await adminSession.logout();
        $q.notify({ type: "positive", message: t("auth.logoutSuccess") });
    } catch {
        $q.notify({ type: "negative", message: t("auth.logoutFailed") });
    }
}

onMounted(() => void adminSession.check());

watch(locale, (value) => {
    document.documentElement.lang = value;
    $q.lang.set(value === "zh-CN" ? langZhCN : langEnUS);
    try {
        localStorage.setItem(localeStorageKey, value);
    } catch {
        // The selected language still applies for the current page.
    }
}, { immediate: true });

watch(tab, (newTab) => {
    switch (newTab) {
        case RouteName.Upload:
            router.push("/").catch(ex => Logger.Error(ex));
            break;
        case RouteName.HFS:
            router.push("/hfs").catch(ex => Logger.Error(ex));
            break;
        case RouteName.Activity:
            router.push("/activity").catch(ex => Logger.Error(ex));
            break;
        case RouteName.Settings:
            router.push("/settings").catch(ex => Logger.Error(ex));
            break;
    }
});

watch(adminSession.authenticated, (isAuthenticated) => {
    if (!isAuthenticated && route.matched.some(record => record.meta.requiresAdmin)) {
        tab.value = RouteName.Upload;
        router.replace("/").catch(ex => Logger.Error(ex));
    }
});
</script>

<style scoped>
.login-card {
    width: min(420px, calc(100vw - 32px));
}
</style>
