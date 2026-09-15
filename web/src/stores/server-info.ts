import axios from "axios";
import { computed, ref } from "vue";
import type { IServerInfoResponseData } from "src/share";

const serverInfo = ref<IServerInfoResponseData>();
const loading = ref(false);
let loadTask: Promise<IServerInfoResponseData | undefined> | undefined;

function toNumber(value: unknown) {
    const n = Number(value);
    return Number.isFinite(n) ? n : undefined;
}

function normalizeServerInfo(payload: unknown): IServerInfoResponseData | undefined {
    if (!payload || typeof payload !== "object") {
        return undefined;
    }

    const raw = payload as Record<string, unknown>;
    const wrapped = raw.data && typeof raw.data === "object"
        ? (raw.data as Record<string, unknown>)
        : raw;

    const uploadRaw = wrapped.upload ?? wrapped.Upload;
    if (!uploadRaw || typeof uploadRaw !== "object") {
        return undefined;
    }

    const upload = uploadRaw as Record<string, unknown>;

    const maxFileCount = toNumber(upload.maxFileCount ?? upload.MaxFileCount);
    const maxImageFileSize = toNumber(upload.maxImageFileSize ?? upload.MaxImageFileSize);
    const maxHfsFileSize = toNumber(upload.maxHfsFileSize ?? upload.MaxHfsFileSize);

    return {
        upload: {
            maxFileCount: maxFileCount ?? 0,
            maxImageFileSize: maxImageFileSize ?? 0,
            maxHfsFileSize: maxHfsFileSize ?? 0,
        },
    };
}

export function useServerInfoStore() {
    async function load(force = false) {
        if (!force && serverInfo.value) {
            return serverInfo.value;
        }

        if (!force && loadTask) {
            return await loadTask;
        }

        loading.value = true;

        loadTask = (async () => {
            try {
                const r = await axios.get("/server/info");
                serverInfo.value = normalizeServerInfo(r.data);

                return serverInfo.value;
            } catch {
                return undefined;
            } finally {
                loading.value = false;
                loadTask = undefined;
            }
        })();

        return await loadTask;
    }

    const upload = computed(() => serverInfo.value?.upload);

    if (!serverInfo.value && !loadTask) {
        load().catch(() => {
            // no-op
        });
    }

    return {
        serverInfo,
        upload,
        loading,
        load,
    };
}
