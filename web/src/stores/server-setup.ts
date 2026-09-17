import axios from "axios";
import { computed, ref } from "vue";

const initialized = ref<boolean | null>(null);
const checking = ref(false);
const initializing = ref(false);
const defaults = ref<InitialApplicationSettings | null>(null);

export interface InitialApplicationSettings {
    uploadDirectory: string;
    downloadDirectory: string;
    publicUploads: boolean;
    maxFileCount: number;
    maxImageFileSize: number;
    maxHFSFileSize: number;
    rememberDurationDays: number;
    bingSyncEnabled: boolean;
    bingSyncIntervalHours: number;
    allowedDomainNames: string[];
    trustedProxyCIDRs: string[];
    hfsRoots: Array<{ name: string; path: string; public: boolean; readOnly?: boolean }>;
    localLogEnabled: boolean;
    logMaxFileSize: number;
    logMaxBackups: number;
}

export function useServerSetupStore() {
    async function check() {
        checking.value = true;
        try {
            const response = await axios.get("/api/v1/setup/status");
            initialized.value = response.status === 200 && response.data?.initialized === true;
            defaults.value = !initialized.value && response.data?.defaults
                ? response.data.defaults as InitialApplicationSettings
                : null;
        } catch (error) {
            if (axios.isAxiosError(error) && error.response?.status === 404) {
                initialized.value = true;
                defaults.value = null;
            } else {
                initialized.value = null;
                defaults.value = null;
            }
        } finally {
            checking.value = false;
        }
        return initialized.value;
    }

    async function initialize(username: string, password: string, settings: InitialApplicationSettings) {
        initializing.value = true;
        try {
            await axios.post("/api/v1/setup", { username, password, settings });
            initialized.value = true;
        } finally {
            initializing.value = false;
        }
    }

    return {
        initialized: computed(() => initialized.value),
        defaults: computed(() => defaults.value),
        checking: computed(() => checking.value),
        initializing: computed(() => initializing.value),
        check,
        initialize,
    };
}

export async function checkServerInitialization() {
    return useServerSetupStore().check();
}
