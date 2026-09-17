<template>
    <q-layout view="hHh lpR fFf">
        <q-page-container>
            <q-page class="setup-page flex flex-center q-pa-md">
                <q-card class="setup-card">
                    <q-card-section class="bg-primary text-white q-pa-lg">
                        <div class="row items-start justify-between no-wrap">
                            <div>
                                <div class="text-h5">{{ t("setup.title") }}</div>
                                <div class="q-mt-sm text-body2">{{ t("setup.description") }}</div>
                            </div>
                            <q-btn-dropdown flat icon="language" :label="locale === 'zh-CN' ? '中文' : 'English'">
                                <q-list class="text-dark">
                                    <q-item v-close-popup clickable @click="setLocale('en-US')"><q-item-section>English</q-item-section></q-item>
                                    <q-item v-close-popup clickable @click="setLocale('zh-CN')"><q-item-section>中文</q-item-section></q-item>
                                </q-list>
                            </q-btn-dropdown>
                        </div>
                    </q-card-section>

                    <q-form @submit.prevent="submit">
                        <q-card-section class="q-pa-lg q-gutter-md">
                            <q-banner rounded class="bg-blue-1 text-primary">
                                {{ t("setup.notice") }}
                            </q-banner>

                            <q-input
                                v-model.trim="username"
                                outlined
                                autofocus
                                :label="t('setup.adminUsername')"
                                name="username"
                                autocomplete="username"
                                maxlength="64"
                                :disable="busy"
                                :rules="usernameRules"
                            />

                            <q-input
                                v-model="password"
                                outlined
                                :label="t('setup.password')"
                                name="new-password"
                                type="password"
                                autocomplete="new-password"
                                :disable="busy"
                                :rules="passwordRules"
                                :hint="t('setup.passwordHint')"
                            />

                            <q-input
                                v-model="confirmation"
                                outlined
                                :label="t('setup.confirmPassword')"
                                name="confirm-password"
                                type="password"
                                autocomplete="new-password"
                                :disable="busy"
                                :rules="confirmationRules"
                            />

                            <q-separator />
                            <div class="text-h6">{{ t("setup.initialSettings") }}</div>
                            <q-toggle v-model="publicUploads" :label="t('fields.publicUploads')" :disable="busy" />
                            <div class="settings-limit-grid">
                                <q-input v-model.trim="uploadDirectory" outlined :label="t('fields.uploadDirectory')" :hint="t('fields.relativeData')" :disable="busy" />
                                <q-input v-model.trim="downloadDirectory" outlined :label="t('fields.downloadDirectory')" :hint="t('fields.relativeData')" :disable="busy" />
                            </div>
                            <div class="settings-limit-grid">
                                <div>
                                    <q-input v-model.number="maxFileCount" outlined type="number" :label="t('fields.filesPerUpload')" min="1" max="1000" :disable="busy" />
                                </div>
                                <div>
                                    <q-input v-model.number="maxImageMiB" outlined type="number" :label="t('fields.maximumImage')" suffix="MiB" min="1" max="1024" :disable="busy" />
                                </div>
                                <div>
                                    <q-input v-model.number="maxHFSMiB" outlined type="number" :label="t('fields.maximumHfsFile')" suffix="MiB" min="1" max="1048576" :disable="busy" />
                                </div>
                            </div>
                            <q-input v-model.number="rememberDurationDays" outlined type="number" :label="t('fields.rememberLogin')" :suffix="t('common.days')" min="1" max="365" :disable="busy" />
                            <div class="text-subtitle1">{{ t("fields.bingArchive") }}</div>
                            <q-toggle v-model="bingSyncEnabled" :label="t('fields.automaticBing')" :disable="busy" />
                            <q-input
                                v-model.number="bingSyncIntervalHours"
                                outlined
                                type="number"
                                :label="t('fields.syncInterval')"
                                :suffix="t('common.hours')"
                                min="1"
                                max="8760"
                                :disable="busy || !bingSyncEnabled"
                            />
                            <q-input v-model="allowedDomainsText" outlined type="textarea" autogrow :label="t('fields.allowedReferers')" :hint="t('fields.refererHint')" :disable="busy" />
                            <div class="text-subtitle1">{{ t("fields.hfsRoots") }}</div>
                            <div v-for="(root, index) in hfsRoots" :key="index" class="hfs-root-grid">
                                <div>
                                    <q-input v-model.trim="root.name" outlined :label="t('common.name')" :disable="busy" />
                                </div>
                                <div>
                                    <q-input v-model.trim="root.path" outlined :label="t('common.path')" :hint="t('fields.hfsPathHint')" :disable="busy" />
                                </div>
                                <div class="hfs-root-action"><q-toggle v-model="root.public" :label="t('common.public')" :disable="busy" /></div>
                                <div class="hfs-root-action"><q-toggle v-model="root.readOnly" :label="t('common.readOnly')" :disable="busy" /></div>
                                <div class="hfs-root-action"><q-btn flat round icon="delete" color="negative" :disable="busy" @click="hfsRoots.splice(index, 1)" /></div>
                            </div>
                            <q-btn flat color="primary" icon="add" :label="t('fields.addHfsRoot')" :disable="busy" @click="hfsRoots.push({ name: '', path: '', public: false, readOnly: false })" />
                        </q-card-section>

                        <q-card-actions class="q-px-lg q-pb-lg" align="right">
                            <q-btn
                                color="primary"
                                :label="t('setup.createAdmin')"
                                type="submit"
                                :loading="busy"
                            />
                        </q-card-actions>
                    </q-form>
                </q-card>
            </q-page>
        </q-page-container>
    </q-layout>
</template>

<script setup lang="ts">
import axios from "axios";
import { useQuasar } from "quasar";
import langEnUS from "quasar/lang/en-US";
import langZhCN from "quasar/lang/zh-CN";
import { localeStorageKey, type SupportedLocale } from "src/boot/i18n";
import { useAdminSessionStore } from "src/stores/admin-session";
import { useServerSetupStore } from "src/stores/server-setup";
import { computed, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { useI18n } from "vue-i18n";

const $q = useQuasar();
const router = useRouter();
const setup = useServerSetupStore();
const adminSession = useAdminSessionStore();
const { t, locale } = useI18n();
const serverDefaults = setup.defaults.value;
const username = ref("admin");
const password = ref("");
const confirmation = ref("");
const publicUploads = ref(serverDefaults?.publicUploads ?? true);
const uploadDirectory = ref(serverDefaults?.uploadDirectory ?? "upload");
const downloadDirectory = ref(serverDefaults?.downloadDirectory ?? "download");
const maxFileCount = ref(serverDefaults?.maxFileCount ?? 100);
const maxImageMiB = ref((serverDefaults?.maxImageFileSize ?? 100 * 1024 * 1024) / 1024 / 1024);
const maxHFSMiB = ref((serverDefaults?.maxHFSFileSize ?? 1000 * 1024 * 1024) / 1024 / 1024);
const rememberDurationDays = ref(serverDefaults?.rememberDurationDays ?? 30);
const bingSyncEnabled = ref(serverDefaults?.bingSyncEnabled ?? true);
const bingSyncIntervalHours = ref(serverDefaults?.bingSyncIntervalHours ?? 24);
const allowedDomainsText = ref((serverDefaults?.allowedDomainNames ?? []).join("\n"));
const hfsRoots = ref((serverDefaults?.hfsRoots ?? []).map(root => ({ ...root })));
const localLogEnabled = serverDefaults?.localLogEnabled ?? true;
const logMaxFileSize = serverDefaults?.logMaxFileSize ?? 10 * 1024 * 1024;
const logMaxBackups = serverDefaults?.logMaxBackups ?? 5;
const submitting = ref(false);
const busy = computed(() => submitting.value || setup.initializing.value);
const usernamePattern = /^[A-Za-z0-9._@-]+$/;

const usernameRules = [
    (value: string) => value.length >= 1 || t("setup.validation.usernameRequired"),
    (value: string) => value.length <= 64 || t("setup.validation.usernameLength"),
    (value: string) => usernamePattern.test(value) || t("setup.validation.usernameCharacters"),
];
const passwordRules = [
    (value: string) => new TextEncoder().encode(value).length >= 16 || t("setup.validation.passwordMinimum"),
    (value: string) => new TextEncoder().encode(value).length <= 1024 || t("setup.validation.passwordMaximum"),
];
const confirmationRules = [
    (value: string) => value === password.value || t("setup.validation.passwordMismatch"),
];

function setLocale(value: SupportedLocale) {
    locale.value = value;
}

watch(locale, (value) => {
    document.documentElement.lang = value;
    $q.lang.set(value === "zh-CN" ? langZhCN : langEnUS);
    try { localStorage.setItem(localeStorageKey, value); } catch { /* applies for this page */ }
}, { immediate: true });

async function submit() {
    submitting.value = true;
    try {
        await setup.initialize(username.value, password.value, {
            uploadDirectory: uploadDirectory.value,
            downloadDirectory: downloadDirectory.value,
            publicUploads: publicUploads.value,
            maxFileCount: maxFileCount.value,
            maxImageFileSize: Math.round(maxImageMiB.value * 1024 * 1024),
            maxHFSFileSize: Math.round(maxHFSMiB.value * 1024 * 1024),
            rememberDurationDays: rememberDurationDays.value,
            bingSyncEnabled: bingSyncEnabled.value,
            bingSyncIntervalHours: bingSyncIntervalHours.value,
            allowedDomainNames: allowedDomainsText.value.split(/\r?\n|,/).map(value => value.trim()).filter(Boolean),
            trustedProxyCIDRs: serverDefaults?.trustedProxyCIDRs ?? [],
            hfsRoots: hfsRoots.value,
            localLogEnabled,
            logMaxFileSize,
            logMaxBackups,
        });
        await waitForRestart();
        await adminSession.login(username.value, password.value);
        password.value = "";
        confirmation.value = "";
        await router.replace("/");
        $q.notify({ type: "positive", message: t("setup.created") });
    } catch (error) {
        const message = axios.isAxiosError(error) && typeof error.response?.data?.error === "string"
            ? error.response.data.error
            : t("setup.failed");
        $q.notify({ type: "negative", message });
    } finally {
        submitting.value = false;
    }
}

async function waitForRestart() {
    const deadline = Date.now() + 30_000;
    while (Date.now() < deadline) {
        await new Promise(resolve => globalThis.setTimeout(resolve, 500));
        if (await setup.check()) {
            return;
        }
    }
    throw new Error(t("setup.restartFailed"));
}
</script>

<style scoped>
.setup-page {
    min-height: 100vh;
    background: #f4f7fb;
}

.setup-card {
    width: min(760px, 100%);
}

.settings-limit-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 16px;
}

.hfs-root-grid {
    display: grid;
    grid-template-columns: minmax(140px, 0.7fr) minmax(220px, 1.2fr) auto auto auto;
    gap: 8px;
    align-items: start;
}

.hfs-root-action {
    padding-top: 8px;
}

@media (max-width: 599px) {
    .settings-limit-grid,
    .hfs-root-grid {
        grid-template-columns: 1fr;
    }

    .hfs-root-action {
        padding-top: 0;
    }
}
</style>
