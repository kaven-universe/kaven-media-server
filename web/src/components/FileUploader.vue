<template>
    <q-uploader :url="urlUpload" :label="t('uploader.title')" multiple accept="image/*" class="uploader" field-name="images"
        :form-fields="ConvertTo(formFields)" @added="added" @uploaded="handleResponse" @failed="handleResponse"
        :batch="batch" :max-files="maxFileCount" :max-file-size="maxImageFileSize">
        <template v-slot:list="scope">
            <q-list separator>
                <q-item v-for="file in scope.files" :key="file.__key" v-ripple>

                    <q-item-section v-if="file.__img" thumbnail class="gt-xs">
                        <img :src="file.__img.src" style="object-fit: scale-down;">
                    </q-item-section>

                    <q-item-section>
                        <q-item-label class="full-width ellipsis">
                            {{ getFileName(file) }}
                        </q-item-label>

                        <q-item-label caption :class="file.statusColor ? `text-${file.statusColor}` : ''"
                            v-if="file.status">
                            {{ file.status }}
                        </q-item-label>

                        <q-item-label caption v-else>
                            {{ file.__sizeLabel }} / {{ file.__progressLabel }}
                        </q-item-label>

                    </q-item-section>

                    <q-item-section side>
                        <FileBar :file="file" v-if="file.link"></FileBar>
                        <FileBar :file="file" v-else>
                            <q-btn flat dense round icon="drive_file_rename_outline" @click="rename(file)" />
                            <q-btn flat dense round icon="delete" @click="scope.removeFile(file)" />
                        </FileBar>
                    </q-item-section>
                </q-item>

            </q-list>
        </template>
    </q-uploader>

    <q-dialog v-model="prompt" persistent>
        <q-card style="min-width: 350px">
            <q-card-section>
                <div class="text-h6">{{ t("uploader.rename") }}</div>
            </q-card-section>

            <q-card-section class="q-pt-none">
                <q-input dense v-model="promptValue" autofocus @keyup.enter="applyNewName" />
            </q-card-section>

            <q-card-actions align="right" class="text-primary">
                <q-btn flat :label="t('common.cancel')" v-close-popup />
                <q-btn flat :label="t('common.ok')" v-close-popup @click="applyNewName" />
            </q-card-actions>
        </q-card>
    </q-dialog>
</template>

<script setup lang="ts">
import FileBar from "components/FileBar.vue";
import { ConvertTo, GenerateGuid, ReplaceAll } from "kaven-basic";
import type { IFile } from "src/common";
import { useAdminSessionStore } from "src/stores/admin-session";
import { useServerInfoStore } from "src/stores/server-info";
import type { IFileAdditionalInfo, IUploadResponseData } from "src/share";
import { ErrorCode, ErrorCodeName } from "src/share";
import { computed, onMounted, ref, type Ref } from "vue";
import { useI18n } from "vue-i18n";

interface IFileEx extends IFile {
    info: Ref<IFileAdditionalInfo>;
    status?: string;
    statusColor?: string;
}

const urlUpload = "/images/upload";

const prompt = ref(false);
const promptValue = ref("");
const currentFile = ref<IFileEx>();
const batch = ref(true);
const serverInfoStore = useServerInfoStore();
const adminSession = useAdminSessionStore();
const { t } = useI18n();
const maxFileCount = computed(() => serverInfoStore.upload.value?.maxFileCount);
const maxImageFileSize = computed(() => serverInfoStore.upload.value?.maxImageFileSize);

async function loadServerInfo() {
    await serverInfoStore.load();
}

function formFields(files: IFileEx[]) {
    const fields = [];

    for (const file of files) {
        fields.push({
            name: file.name,
            value: JSON.stringify(file.info.value),
        });
    }

    return fields;
}

function added(files: readonly IFileEx[]) {
    for (const file of files) {
        const info: IFileAdditionalInfo = {
            uuid: ReplaceAll(GenerateGuid(), "-", ""),
        };

        file.info = ref<IFileAdditionalInfo>(info);
    }
}

function handleResponse(info: {
    files: readonly IFileEx[];
    xhr: XMLHttpRequest;
}) {
    if (info.xhr.status === 401) {
        adminSession.clear();
        for (const file of info.files) {
            file.status = t("uploader.loginRequired");
            file.statusColor = "negative";
        }
        return;
    }

    if (info.xhr.status === 0 || !info.xhr.responseText.trim()) {
        for (const file of info.files) {
            file.status = info.xhr.status === 0 ? t("uploader.connectionLost") : t("uploader.emptyResponse");
            file.statusColor = "negative";
        }
        return;
    }

    const update = (r: IUploadResponseData, file?: IFileEx) => {
        if (!file) {
            return;
        }

        delete file.status;
        delete file.statusColor;

        if (r.errorCode === ErrorCode.InvalidFileType) {
            file.status = t("uploader.notImage");
            file.statusColor = "negative";
        } else if (r.errorCode === ErrorCode.FileAlreadyExist) {
            file.status = t("uploader.alreadyExists");
            file.statusColor = "warning";
        } else if (r.errorCode === ErrorCode.UnexpectedError) {
            file.status = t("uploader.serverError");
            file.statusColor = "negative";
        } else if (r.errorCode === ErrorCode.None) {
            file.status = t("uploader.uploaded");
            file.statusColor = "positive";
        } else {
            file.status = ErrorCodeName(r.errorCode);
            file.statusColor = "grey-7";
        }

        if (r.image) {
            const uuid = r.image.uuid;
            const id = r.image.sha1 || uuid;
            const origin = window.location.origin;
            const url = `${origin}/image/${id}`;

            file.link = url;
        }
    };

    let json: IUploadResponseData | IUploadResponseData[];
    try {
        json = JSON.parse(info.xhr.responseText) as IUploadResponseData | IUploadResponseData[];
    } catch {
        for (const file of info.files) {
            file.status = t("uploader.invalidResponse", { status: info.xhr.status || t("uploader.networkError") });
            file.statusColor = "negative";
        }
        return;
    }
    if (import.meta.env.DEV) {
        console.log(json);
    }

    if (Array.isArray(json)) {
        json.forEach((r: IUploadResponseData) => {
            const file = info.files.find(p => p.info.value.uuid === r.image?.uuid);
            update(r, file);
        });
    } else {
        info.files.forEach(file => update(json, file));
    }
}

function rename(file: IFileEx) {
    currentFile.value = file;
    promptValue.value = file.info.value.name ?? file.name;
    prompt.value = true;
}

function applyNewName() {
    if (currentFile.value) {
        currentFile.value.info.value.name = promptValue.value;
        currentFile.value = undefined;
        prompt.value = false;
    }
}

function getFileName(file: IFileEx): string {
    return file.info.value.name || file.name;
}

onMounted(() => {
    loadServerInfo().catch(() => {
        // no-op
    });
});
</script>

<style lang="scss" scoped>
.uploader {
    width: 100%;
    height: 100%;
    max-width: unset;
    max-height: unset;
}
</style>
