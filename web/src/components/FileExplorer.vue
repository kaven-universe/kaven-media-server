<template>
    <div>
        <q-table :rows="files" :columns="columns" :rows-per-page-options="[0]" :pagination="pagination"
            :loading="loading" row-key="Link" style="height: calc(100vh - 80px)" virtual-scroll>

            <template v-slot:top>
                <q-linear-progress v-if="isUploading" class="top-border-progress" :value="uploadProgress / 100"
                    color="primary" size="4px" />

                <q-toolbar class="no-padding">
                    <!-- <q-btn flat round dense icon="menu" /> -->
                    <q-toolbar-title>
                        <q-breadcrumbs gutter="xs">
                            <template v-for="name, index in paths" :key="index">
                                <q-breadcrumbs-el>
                                    <span class="cursor-pointer" @click="openPath(index)">
                                        {{ index === 0 ? "HFS" : name }}
                                    </span>
                                </q-breadcrumbs-el>
                            </template>
                        </q-breadcrumbs>
                    </q-toolbar-title>
                    <q-btn flat round dense icon="create_new_folder" @click="promptCreateFolder"
                        :disable="loading" />
                    <q-btn flat round dense icon="upload" @click="pickUploadFiles" :disable="loading" />
                    <q-btn flat round dense icon="refresh" @click="open" :disable="loading" />
                </q-toolbar>
            </template>

            <template v-slot:header="props">
                <q-tr :props="props">
                    <q-th v-for="col in props.cols" :key="col.name" :props="props" class="text-italic text-purple">
                        {{ col.label }}
                    </q-th>
                </q-tr>

                <q-tr>
                    <q-th class="text-left">
                        <span :class="buildIconClass(parentDirectory.Icon)">
                        </span>
                    </q-th>
                    <q-th :colspan="colspan" class="text-left">
                        <span class="cursor-pointer" @click="openFile(parentDirectory)">
                            {{ parentDirectory.name }}
                        </span>
                    </q-th>
                </q-tr>
            </template>

            <template v-slot:body-cell-Icon="props">
                <q-td :props="props">
                    <span :class="buildIconClass(props.value)">
                        <!-- <q-tooltip>
                            {{ props.value }}
                        </q-tooltip> -->
                    </span>
                </q-td>
            </template>

            <template v-slot:body-cell-Name="props">
                <q-td :props="props">
                    <span class="cursor-pointer" @click="openFile(props.row)">{{ props.value }}</span>
                </q-td>
            </template>

            <template v-slot:body-cell-LastModified="props">
                <q-td :props="props">
                    <span v-if="props.value">
                        {{ FormatDate(props.row.LastModifiedDate) }}
                        <q-tooltip>
                            {{ props.value }}
                        </q-tooltip>
                    </span>
                </q-td>
            </template>

            <template v-slot:body-cell-Size="props">
                <q-td :props="props">
                    <span v-if="props.value >= 0">
                        {{ ToFileSize(props.value, true) }}
                        <q-tooltip>
                            {{ props.value }}
                        </q-tooltip>
                    </span>
                    <span v-else>
                        -
                    </span>
                </q-td>
            </template>

            <template v-slot:body-cell-Action="props">
                <q-td :props="props">
                    <FileBar :file="props.row" v-if="!props.row.isDirectory">
                    </FileBar>
                </q-td>
            </template>

            <template v-slot:pagination>
                <span>{{ files.length }} items</span>
            </template>
        </q-table>

        <input ref="uploadInput" type="file" multiple class="hidden" @change="uploadFiles">

        <ImageViewer v-model="imageViewer" :images="images"></ImageViewer>

    </div>
</template>

<script setup lang="ts">
import axios from "axios";
import FileBar from "components/FileBar.vue";
import ImageViewer from "components/ImageViewer.vue";
import { CombinePath, FormatDate, GetBaseDir, HttpStatusCode, ToFileSize } from "kaven-basic";
import { useQuasar } from "quasar";
import type { IFile } from "src/common";
import { HfsFile, Logger } from "src/common";
import type { IHfsFileInfo, IUploadResponseData } from "src/share";
import { ErrorCode, ErrorCodeName, HfsFileInfoComparer, IsImage } from "src/share";
import { useServerInfoStore } from "src/stores/server-info";
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

/* eslint-disable */
interface IColumn {
    name: string;
    label: string;
    field: string | ((row: any) => any);
    required?: boolean;
    align?: "left" | "right" | "center";
    sortable?: boolean;
    sort?: (a: any, b: any, rowA: any, rowB: any) => number;
    sortOrder?: "ad" | "da";
    format?: (val: any, row: any) => any;
    style?: string | ((row: any) => string);
    classes?: string | ((row: any) => string);
    headerStyle?: string;
    headerClasses?: string;
}
/* eslint-enable */

const props = defineProps<{
    initialPath?: string;
}>();

const initialPath = props.initialPath ?? "/";

const pagination = {
    rowsPerPage: 0, // 0 means all rows
};

const columns: IColumn[] = [
    { name: "Icon", label: "", align: "left", field: "Icon", sortable: true },
    { name: "Name", label: "Name", align: "left", field: (row: HfsFile) => row.name, sortable: true },
    { name: "LastModified", label: "Last modified", align: "left", field: "lastModified", sortable: true },
    { name: "Size", label: "Size", align: "left", field: "size", sortable: true },
    { name: "Action", label: "Action", align: "left", field: () => "" },
];

// reactive state
const files = ref<HfsFile[]>([]);
const loading = ref(false);
const imageViewer = ref(false);
const isUploading = ref(false);
const uploadProgress = ref(0);
const uploadCurrentFile = ref(0);
const uploadTotalFiles = ref(0);
const serverInfoStore = useServerInfoStore();
const $q = useQuasar();

const route = useRoute();
const router = useRouter();

// computed
const hfsPaths = computed(() => {
    const path = route.params.hfsPath;
    if (typeof path === "string") return [path];
    if (Array.isArray(path)) return path;
    return [];
});

const hfsPath = computed(() => hfsPaths.value.join("/"));

const parentLink = computed(() =>
    CombinePath(initialPath, hfsPath.value.includes("/") ? GetBaseDir(hfsPath.value, true) : ""),
);

const currentPathUrl = computed(() => CombinePath(initialPath, hfsPath.value));

const title = computed(() => `Index of ${hfsPath.value}`);

const paths = computed(() => ["", ...hfsPaths.value]);

const parentDirectory = computed(
    () =>
        new HfsFile({
            name: "Parent Directory",
            link: parentLink.value,
            isDirectory: true,
            lastModified: "",
        }),
);

const images = computed<IFile[]>(() => files.value.filter(p => IsImage(p.name)));

const colspan = columns.length - 1;
const uploadInput = ref<HTMLInputElement>();

async function loadServerInfo() {
    await serverInfoStore.load();
}

function formatFileNames(inputFiles: File[], max = 5) {
    const names = inputFiles.map(file => file.name);
    const preview = names.slice(0, max).join(", ");

    if (names.length <= max) {
        return preview;
    }

    return `${preview} (+${names.length - max} more)`;
}

function applyUploadLimits(selected: FileList) {
    let files = Array.from(selected);
    const uploadConfig = serverInfoStore.upload.value;

    if (uploadConfig?.maxFileCount && files.length > uploadConfig.maxFileCount) {
        const skipped = files.length - uploadConfig.maxFileCount;
        const skippedFiles = files.slice(uploadConfig.maxFileCount);
        files = files.slice(0, uploadConfig.maxFileCount);

        $q.notify({
            type: "warning",
            message: `${skipped} file(s) skipped (max ${uploadConfig.maxFileCount}): ${formatFileNames(skippedFiles)}.`,
            position: "top-right",
            timeout: 0,
            actions: [{ icon: "close", color: "white" }],
        });
    }

    if (uploadConfig?.maxHfsFileSize) {
        const tooLargeFiles = files.filter(file => file.size > uploadConfig.maxHfsFileSize);
        files = files.filter(file => file.size <= uploadConfig.maxHfsFileSize);
        const skipped = tooLargeFiles.length;

        if (skipped > 0) {
            $q.notify({
                type: "warning",
                message: `${skipped} file(s) exceed max size (${ToFileSize(uploadConfig.maxHfsFileSize, true)}): ${formatFileNames(tooLargeFiles)}.`,
                position: "top-right",
                timeout: 0,
                actions: [{ icon: "close", color: "white" }],
            });
        }
    }

    return files;
}

function normalizeUploadResponses(data: unknown) {
    if (Array.isArray(data)) {
        return data as IUploadResponseData[];
    }

    return [data as IUploadResponseData];
}

function notifyUploadErrors(fileName: string, responses: IUploadResponseData[]) {
    responses
        .filter(p => p.errorCode !== ErrorCode.None)
        .forEach(p => {
            $q.notify({
                type: "negative",
                message: `${fileName}: ${uploadErrorMessage(p)}.`,
                position: "top-right",
                timeout: 0,
                actions: [{ icon: "close", color: "white" }],
            });
        });
}

function uploadErrorMessage(r: IUploadResponseData, isFolder = false) {
    if (r.errorCode === ErrorCode.InvalidFileType) {
        return "Not a supported file type";
    }

    if (r.errorCode === ErrorCode.FileAlreadyExist) {
        return isFolder ? "Folder already exists" : "File already exists";
    }

    if (r.errorCode === ErrorCode.FileTooLarge) {
        return "File is too large";
    }

    if (r.errorCode === ErrorCode.FolderNotFound) {
        return "Folder no longer exists, please refresh";
    }

    if (r.errorCode === ErrorCode.UnexpectedError) {
        return "Server error";
    }

    return ErrorCodeName(r.errorCode);
}

// methods
function buildIconClass(icon: string) {
    return `icon fiv-sqo fiv-icon-${icon}`;
}

async function open() {
    loading.value = true;
    try {
        const r = await axios.get(currentPathUrl.value);

        if (r.status === HttpStatusCode.OK) {
            const result = r.data as IHfsFileInfo[];
            result.sort(HfsFileInfoComparer);
            files.value = result.map(p => new HfsFile(p));
        } else {
            console.warn(r.data);
            $q.notify({
                type: "negative",
                message: "Failed to refresh: unexpected response from server.",
                position: "top-right",
                timeout: 0,
                actions: [{ icon: "close", color: "white" }],
            });
        }
    } catch (ex) {
        Logger.Error(ex);
        $q.notify({
            type: "negative",
            message: "Failed to refresh.",
            position: "top-right",
            timeout: 0,
            actions: [{ icon: "close", color: "white" }],
        });
    } finally {
        loading.value = false;
    }
}

async function pickUploadFiles() {
    await loadServerInfo();
    uploadInput.value?.click();
}

function promptCreateFolder() {
    $q.dialog({
        title: "Create folder",
        prompt: {
            model: "",
            isValid: val => !!val.trim() && !/[\\/]/.test(val),
            type: "text",
        },
        cancel: true,
        persistent: true,
    }).onOk((name: string) => {
        createFolder(name.trim()).catch(ex => Logger.Error(ex));
    });
}

async function createFolder(name: string) {
    loading.value = true;

    try {
        const r = await axios.post(`${currentPathUrl.value}/${encodeURIComponent(name)}`, undefined, {
            params: { mkdir: true },
        });

        const data = r.data as IUploadResponseData;
        if (data.errorCode !== ErrorCode.None) {
            $q.notify({
                type: "negative",
                message: `${name}: ${uploadErrorMessage(data, true)}.`,
                position: "top-right",
                timeout: 0,
                actions: [{ icon: "close", color: "white" }],
            });
            return;
        }

        await open();
    } catch (ex) {
        const responseData = axios.isAxiosError(ex) ? (ex.response?.data as IUploadResponseData) : undefined;

        $q.notify({
            type: "negative",
            message: `${name}: ${responseData ? uploadErrorMessage(responseData, true) : "Create folder failed"}.`,
            position: "top-right",
            timeout: 0,
            actions: [{ icon: "close", color: "white" }],
        });
    } finally {
        loading.value = false;
    }
}

async function uploadFiles(e: Event) {
    const input = e.target as HTMLInputElement;
    const selected = input.files;

    if (!selected || selected.length === 0) {
        return;
    }

    const limitedFiles = applyUploadLimits(selected);

    if (limitedFiles.length === 0) {
        $q.notify({
            type: "negative",
            message: "No files to upload after applying upload limits.",
            position: "top-right",
            timeout: 0,
            actions: [{ icon: "close", color: "white" }],
        });

        input.value = "";
        return;
    }

    loading.value = true;
    isUploading.value = true;
    uploadProgress.value = 0;
    uploadCurrentFile.value = 0;
    uploadTotalFiles.value = limitedFiles.length;

    try {
        for (const [index, file] of limitedFiles.entries()) {
            uploadCurrentFile.value = index + 1;

            const formData = new FormData();
            formData.append("file", file, file.name);

            try {
                const r = await axios.post(currentPathUrl.value, formData, {
                    headers: {
                        "Content-Type": "multipart/form-data",
                    },
                    onUploadProgress: progressEvent => {
                        const fileProgress = progressEvent.total
                            ? progressEvent.loaded / progressEvent.total
                            : 0;

                        uploadProgress.value =
                            ((index + fileProgress) / uploadTotalFiles.value) * 100;
                    },
                });

                const responses = normalizeUploadResponses(r.data);
                notifyUploadErrors(file.name, responses);
            } catch (ex) {
                const responseData = axios.isAxiosError(ex) ? ex.response?.data : undefined;

                if (responseData) {
                    const responses = normalizeUploadResponses(responseData);
                    notifyUploadErrors(file.name, responses);
                } else {
                    Logger.Error(ex);
                    $q.notify({
                        type: "negative",
                        message: `${file.name}: Upload failed.`,
                        position: "top-right",
                        timeout: 0,
                        actions: [{ icon: "close", color: "white" }],
                    });
                }
            }

            uploadProgress.value = ((index + 1) / uploadTotalFiles.value) * 100;
        }

        await open();
    } finally {
        input.value = "";
        isUploading.value = false;
        loading.value = false;
    }
}

function openFile(file: HfsFile) {
    if (file.Icon === "folder") {
        router.push(file.link).catch(ex => Logger.Error(ex));
    } else {
        window.open(file.link, "_blank");
    }
}

function openPath(index: number) {
    const path = paths.value.slice(0, index + 1).join("/");
    router.push(initialPath + path).catch(ex => Logger.Error(ex));
}

// watchers & lifecycle
watch(hfsPath, () => {
    open().catch(ex => Logger.Error(ex));
});

onMounted(() => {
    open().catch(ex => Logger.Error(ex));
});
</script>

<style lang="scss">
@import "file-icon-vectors/dist/file-icon-vectors.min.css";
</style>

<style lang="scss" scoped>
.icon {
    font-size: 2rem;
}

.cursor-pointer:hover {
    text-decoration: underline;
}

.top-border-progress {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    z-index: 2;
}
</style>
