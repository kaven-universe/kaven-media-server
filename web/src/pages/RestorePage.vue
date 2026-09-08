<template>
    <q-page class="q-pa-md restore-page">
        <q-card flat bordered class="restore-card">
            <q-card-section>
                <div class="text-h5">Restore backup</div>
                <p class="text-body2 text-grey-7 q-mb-none">
                    Select the backup folder containing <code>manifest.json</code> and
                    <code>data</code>. The server validates every checksum before replacing
                    the current data and restarts itself after the upload succeeds.
                </p>
            </q-card-section>

            <q-separator />

            <q-card-section>
                <q-banner rounded class="bg-orange-1 text-orange-10 q-mb-md">
                    This replaces the active database and managed files. Keep your original
                    backup until you have checked the restored site. Administrator login is required.
                </q-banner>

                <input
                    ref="directoryInput"
                    class="hidden"
                    type="file"
                    multiple
                    webkitdirectory
                    @change="selectDirectory"
                >

                <div v-if="files.length > 0" class="q-mb-md">
                    <div><strong>Folder:</strong> {{ folderName }}</div>
                    <div><strong>Files:</strong> {{ files.length.toLocaleString() }}</div>
                    <div><strong>Size:</strong> {{ formatBytes(totalBytes) }}</div>
                </div>

                <q-linear-progress
                    v-if="uploading"
                    :value="progress"
                    color="primary"
                    rounded
                    size="12px"
                    class="q-mb-md"
                />

                <div class="row q-gutter-sm">
                    <q-btn
                        color="primary"
                        outline
                        label="Select backup folder"
                        :disable="uploading"
                        @click="directoryInput?.click()"
                    />
                    <q-btn
                        color="negative"
                        label="Validate and restore"
                        :disable="files.length === 0"
                        :loading="uploading"
                        @click="confirmRestore"
                    />
                </div>
            </q-card-section>
        </q-card>
    </q-page>
</template>

<script setup lang="ts">
import axios from "axios";
import { useQuasar } from "quasar";
import { computed, ref } from "vue";

const $q = useQuasar();
const directoryInput = ref<HTMLInputElement>();
const files = ref<File[]>([]);
const folderName = ref("");
const uploading = ref(false);
const progress = ref(0);
const totalBytes = computed(() => files.value.reduce((total, file) => total + file.size, 0));

function relativePath(file: File) {
    const segments = file.webkitRelativePath.split("/").filter(Boolean);
    return segments.slice(1).join("/");
}

function selectDirectory(event: Event) {
    const input = event.target as HTMLInputElement;
    const selected = Array.from(input.files ?? []);
    input.value = "";
    if (selected.length === 0) {
        return;
    }
    const root = selected[0]?.webkitRelativePath.split("/")[0] ?? "";
    const paths = selected.map(relativePath);
    if (!root || !paths.includes("manifest.json") || !paths.some(path => path.startsWith("data/"))) {
        files.value = [];
        folderName.value = "";
        $q.notify({ type: "negative", message: "Select a backup folder containing manifest.json and data." });
        return;
    }
    files.value = selected;
    folderName.value = root;
    progress.value = 0;
}

function confirmRestore() {
    $q.dialog({
        title: "Replace current data?",
        message: `Restore ${files.value.length.toLocaleString()} files from ${folderName.value}? The server will restart.`,
        cancel: true,
        persistent: true,
        ok: { label: "Restore", color: "negative" },
    }).onOk(() => void restore());
}

async function restore() {
    uploading.value = true;
    progress.value = 0;
    try {
        const form = new FormData();
        for (const file of files.value) {
            form.append(relativePath(file), file, file.name);
        }
        await axios.post("/api/v1/admin/restore", form, {
            timeout: 0,
            onUploadProgress: event => {
                progress.value = event.total ? event.loaded / event.total : 0;
            },
        });
        progress.value = 1;
        $q.notify({ type: "positive", message: "Backup validated. Applying restored data…", timeout: 0 });
        await waitForRestart();
        window.location.hash = "#/";
        window.location.reload();
    } catch (error) {
        const message = axios.isAxiosError(error)
            ? (error.response?.data as { error?: string } | undefined)?.error ?? error.message
            : "Restore failed";
        $q.notify({ type: "negative", message, timeout: 0, actions: [{ icon: "close", color: "white" }] });
    } finally {
        uploading.value = false;
    }
}

async function waitForRestart() {
    let observedOffline = false;
    for (let attempt = 0; attempt < 120; attempt++) {
        await new Promise(resolve => window.setTimeout(resolve, 1000));
        try {
            await axios.get("/healthz", { timeout: 2000 });
            if (observedOffline || attempt > 2) {
                return;
            }
        } catch {
            observedOffline = true;
        }
    }
    throw new Error("Restore was accepted, but the server did not return within two minutes.");
}

function formatBytes(bytes: number) {
    if (bytes < 1024) {
        return `${bytes} B`;
    }
    const units = ["KiB", "MiB", "GiB", "TiB"];
    let value = bytes;
    let unit = -1;
    do {
        value /= 1024;
        unit++;
    } while (value >= 1024 && unit < units.length - 1);
    return `${value.toFixed(value >= 10 ? 1 : 2)} ${units[unit]}`;
}
</script>

<style scoped>
.restore-page {
    display: flex;
    justify-content: center;
}

.restore-card {
    width: 100%;
    max-width: 760px;
    align-self: flex-start;
}
</style>
