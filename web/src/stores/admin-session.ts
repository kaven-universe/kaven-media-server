import axios from "axios";
import { computed, ref } from "vue";

const storageKey = "kaven-media.admin-authenticated";

function readStoredState() {
    try {
        return globalThis.sessionStorage?.getItem(storageKey) === "true";
    } catch {
        return false;
    }
}

const authenticated = ref(readStoredState());
const authenticating = ref(false);
const checking = ref(false);

function storeState(value: boolean) {
    authenticated.value = value;
    try {
        if (value) {
            globalThis.sessionStorage?.setItem(storageKey, "true");
        } else {
            globalThis.sessionStorage?.removeItem(storageKey);
        }
    } catch {
        // The backend remains the authorization boundary when storage is unavailable.
    }
}

export function useAdminSessionStore() {
    async function check() {
        checking.value = true;
        try {
            const response = await axios.get("/api/v1/admin/session");
            const confirmed = response.status === 200 && response.data?.authenticated === true;
            storeState(confirmed);
            return confirmed;
        } catch (error) {
            if (axios.isAxiosError(error) && (error.response?.status === 401 || error.response?.status === 404)) {
                storeState(false);
            }
            return authenticated.value;
        } finally {
            checking.value = false;
        }
    }

    async function login(username: string, password: string, remember = false) {
        authenticating.value = true;
        try {
            const response = await axios.post("/api/v1/admin/login", { username, password, remember });
            if (response.status !== 200 || response.data?.authenticated !== true) {
                throw new Error("The server did not confirm administrator authentication.");
            }
            storeState(true);
        } catch (error) {
            storeState(false);
            throw error;
        } finally {
            authenticating.value = false;
        }
    }

    async function logout() {
        try {
            await axios.post("/api/v1/admin/logout");
            storeState(false);
        } catch (error) {
            if (axios.isAxiosError(error) && (error.response?.status === 401 || error.response?.status === 404)) {
                storeState(false);
                return;
            }
            throw error;
        }
    }

    return {
        authenticated: computed(() => authenticated.value),
        authenticating: computed(() => authenticating.value),
        checking: computed(() => checking.value),
        check,
        login,
        logout,
        clear: () => storeState(false),
    };
}

export function isAdminAuthenticated() {
    return authenticated.value;
}

export async function checkAdminAuthentication() {
    return useAdminSessionStore().check();
}
