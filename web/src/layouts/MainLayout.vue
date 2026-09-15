<template>
    <q-layout view="lHh Lpr lFf">
        <q-header elevated>
            <q-toolbar>
                <q-toolbar-title>
                    Kaven Media Server
                </q-toolbar-title>

                <q-space />

                <q-btn-toggle v-model="tab" flat stretch toggle-color="yellow" :options="options" />

                <q-btn
                    v-if="!adminSession.authenticated.value"
                    class="q-ml-sm"
                    flat
                    icon="login"
                    label="Login"
                    :loading="adminSession.authenticating.value || adminSession.checking.value"
                    :disable="adminSession.checking.value"
                    @click="openLogin"
                />
                <div v-else class="row items-center no-wrap q-ml-sm">
                    <q-chip color="white" text-color="primary" icon="person">
                        Admin
                    </q-chip>
                    <q-btn
                        flat
                        icon="logout"
                        label="Logout"
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
                        <div class="text-h6">Administrator login</div>
                    </q-card-section>

                    <q-card-section class="q-pt-none q-gutter-md">
                        <q-input
                            v-model="loginUsername"
                            autofocus
                            outlined
                            label="Username"
                            name="username"
                            autocomplete="username"
                            :disable="adminSession.authenticating.value"
                        />
                        <q-input
                            v-model="loginPassword"
                            outlined
                            label="Password"
                            name="password"
                            type="password"
                            autocomplete="current-password"
                            :disable="adminSession.authenticating.value"
                        />
                        <q-checkbox
                            v-model="rememberLogin"
                            label="Keep me signed in"
                            :disable="adminSession.authenticating.value"
                        />
                    </q-card-section>

                    <q-card-actions align="right">
                        <q-btn v-close-popup flat label="Cancel" :disable="adminSession.authenticating.value" />
                        <q-btn
                            color="primary"
                            label="Login"
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
import { GetRouteByName, Logger, RouteName } from "src/common";
import { useAdminSessionStore } from "src/stores/admin-session";
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

const $q = useQuasar();
const route = useRoute();
const router = useRouter();
const adminSession = useAdminSessionStore();
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
        label: name,
        value: GetRouteByName(name),
    })));

function openLogin() {
    loginPassword.value = "";
    loginOpen.value = true;
}

async function login() {
    try {
        await adminSession.login(loginUsername.value, loginPassword.value, rememberLogin.value);
        loginPassword.value = "";
        loginOpen.value = false;
        $q.notify({ type: "positive", message: "Administrator login successful." });
    } catch (error) {
        const status = axios.isAxiosError(error) ? error.response?.status : undefined;
        let message = "Administrator login failed.";
        if (status === 404) {
            message = "Administrator login is not configured on this server.";
        } else if (status === 429) {
            message = "Too many login attempts. Try again later.";
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
        $q.notify({ type: "positive", message: "Administrator session ended." });
    } catch {
        $q.notify({ type: "negative", message: "Logout failed. Try again." });
    }
}

onMounted(() => void adminSession.check());

watch(tab, (newTab) => {
    switch (newTab) {
        case RouteName.Upload:
            router.push("/").catch(ex => Logger.Error(ex));
            break;
        case RouteName.HFS:
            router.push("/hfs").catch(ex => Logger.Error(ex));
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
