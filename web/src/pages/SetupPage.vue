<template>
    <q-layout view="hHh lpR fFf">
        <q-page-container>
            <q-page class="setup-page flex flex-center q-pa-md">
                <q-card class="setup-card">
                    <q-card-section class="bg-primary text-white q-pa-lg">
                        <div class="text-h5">Set up Kaven Media Server</div>
                        <div class="q-mt-sm text-body2">
                            Create the administrator account for this server.
                        </div>
                    </q-card-section>

                    <q-form @submit.prevent="submit">
                        <q-card-section class="q-pa-lg q-gutter-md">
                            <q-banner rounded class="bg-blue-1 text-primary">
                                This page is available only before the first administrator is created.
                            </q-banner>

                            <q-input
                                v-model.trim="username"
                                outlined
                                autofocus
                                label="Administrator username"
                                name="username"
                                autocomplete="username"
                                maxlength="64"
                                :disable="busy"
                                :rules="usernameRules"
                            />

                            <q-input
                                v-model="password"
                                outlined
                                label="Password"
                                name="new-password"
                                type="password"
                                autocomplete="new-password"
                                :disable="busy"
                                :rules="passwordRules"
                                hint="Use at least 16 characters."
                            />

                            <q-input
                                v-model="confirmation"
                                outlined
                                label="Confirm password"
                                name="confirm-password"
                                type="password"
                                autocomplete="new-password"
                                :disable="busy"
                                :rules="confirmationRules"
                            />

                            <q-separator />
                            <div class="text-h6">Initial application settings</div>
                            <q-toggle v-model="publicUploads" label="Allow image uploads without administrator login" :disable="busy" />
                            <div class="settings-limit-grid">
                                <q-input v-model.trim="uploadDirectory" outlined label="Upload directory" hint="Relative to the data directory" :disable="busy" />
                                <q-input v-model.trim="downloadDirectory" outlined label="Download directory" hint="Relative to the data directory" :disable="busy" />
                            </div>
                            <div class="settings-limit-grid">
                                <div>
                                    <q-input v-model.number="maxFileCount" outlined type="number" label="Files per upload" min="1" max="1000" :disable="busy" />
                                </div>
                                <div>
                                    <q-input v-model.number="maxImageMiB" outlined type="number" label="Maximum image" suffix="MiB" min="1" max="1024" :disable="busy" />
                                </div>
                                <div>
                                    <q-input v-model.number="maxHFSMiB" outlined type="number" label="Maximum HFS file" suffix="MiB" min="1" max="1048576" :disable="busy" />
                                </div>
                            </div>
                            <q-input v-model.number="rememberDurationDays" outlined type="number" label="Keep remembered login for" suffix="days" min="1" max="365" :disable="busy" />
                            <div class="text-subtitle1">Bing wallpaper archive</div>
                            <q-toggle v-model="bingSyncEnabled" label="Automatically download Bing wallpapers" :disable="busy" />
                            <q-input
                                v-model.number="bingSyncIntervalHours"
                                outlined
                                type="number"
                                label="Synchronization interval"
                                suffix="hours"
                                min="1"
                                max="8760"
                                :disable="busy || !bingSyncEnabled"
                            />
                            <q-input v-model="allowedDomainsText" outlined type="textarea" autogrow label="Allowed image referer domains" hint="One hostname per line. Leave empty to allow all referers." :disable="busy" />
                            <div class="text-subtitle1">HFS roots</div>
                            <div v-for="(root, index) in hfsRoots" :key="index" class="hfs-root-grid">
                                <div>
                                    <q-input v-model.trim="root.name" outlined label="Name" :disable="busy" />
                                </div>
                                <div>
                                    <q-input v-model.trim="root.path" outlined label="Path" hint="Relative to the data directory, or an absolute server path" :disable="busy" />
                                </div>
                                <div class="hfs-root-action"><q-toggle v-model="root.public" label="Public" :disable="busy" /></div>
                                <div class="hfs-root-action"><q-toggle v-model="root.readOnly" label="Read only" :disable="busy" /></div>
                                <div class="hfs-root-action"><q-btn flat round icon="delete" color="negative" :disable="busy" @click="hfsRoots.splice(index, 1)" /></div>
                            </div>
                            <q-btn flat color="primary" icon="add" label="Add HFS root" :disable="busy" @click="hfsRoots.push({ name: '', path: '', public: false, readOnly: false })" />
                        </q-card-section>

                        <q-card-actions class="q-px-lg q-pb-lg" align="right">
                            <q-btn
                                color="primary"
                                label="Create administrator"
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
import { useAdminSessionStore } from "src/stores/admin-session";
import { useServerSetupStore } from "src/stores/server-setup";
import { computed, ref } from "vue";
import { useRouter } from "vue-router";

const $q = useQuasar();
const router = useRouter();
const setup = useServerSetupStore();
const adminSession = useAdminSessionStore();
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
const submitting = ref(false);
const busy = computed(() => submitting.value || setup.initializing.value);
const usernamePattern = /^[A-Za-z0-9._@-]+$/;

const usernameRules = [
    (value: string) => value.length >= 1 || "Enter a username.",
    (value: string) => value.length <= 64 || "Use no more than 64 characters.",
    (value: string) => usernamePattern.test(value) || "Use letters, numbers, dot, underscore, @, or hyphen.",
];
const passwordRules = [
    (value: string) => new TextEncoder().encode(value).length >= 16 || "Use at least 16 characters.",
    (value: string) => new TextEncoder().encode(value).length <= 1024 || "Use no more than 1024 bytes.",
];
const confirmationRules = [
    (value: string) => value === password.value || "Passwords do not match.",
];

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
            hfsRoots: hfsRoots.value,
        });
        await waitForRestart();
        await adminSession.login(username.value, password.value);
        password.value = "";
        confirmation.value = "";
        await router.replace("/");
        $q.notify({ type: "positive", message: "Administrator account created." });
    } catch (error) {
        const message = axios.isAxiosError(error) && typeof error.response?.data?.error === "string"
            ? error.response.data.error
            : "Initialization failed. Try again.";
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
    throw new Error("The server did not restart after initialization.");
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
