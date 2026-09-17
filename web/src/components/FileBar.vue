<template>
    <div class="text-grey-8 q-gutter-xs">
        <q-btn v-for="item, index in internalItems" :key="index" flat dense round :icon="item.icon"
            @click="item.onClick(file)">
            <q-tooltip v-if="!disableTooltip && item.tooltip">
                {{ item.tooltip }}
            </q-tooltip>
        </q-btn>

        <slot></slot>

        <ImageViewer v-model="imageViewer" :images="images"></ImageViewer>
    </div>
</template>

<script setup lang="ts">
import { ref, watch } from "vue";
import ImageViewer from "components/ImageViewer.vue";
import { AddQueryParameterToURL } from "kaven-basic";
import type { IFile } from "src/common";
import { CopyFile, Download, OpenInNewTab, UrlType } from "src/common";
import { IsImage } from "src/share";
import { useI18n } from "vue-i18n";

export interface IFileBarItem {
    icon: string;
    tooltip?: string;
    onClick: (file: IFile) => void;
}

const props = defineProps<{
    file: IFile;
    items?: IFileBarItem[];
    disableTooltip?: boolean;
}>();

const internalItems = ref<IFileBarItem[]>([]);
const { t, locale } = useI18n();
const imageViewer = ref(false);
const images: IFile[] = [];

function rebuildItems() {
    internalItems.value = [];
    images.length = 0;
    if (props.items && props.items.length > 0) {
        internalItems.value.push(...props.items);
    } else if (props.file.link) {
        internalItems.value.push({
            icon: "open_in_new",
            tooltip: t("fileBar.open"),
            onClick: file => OpenInNewTab(file.link),
        });

        internalItems.value.push({
            icon: "download",
            tooltip: t("fileBar.download"),
            onClick: file => Download(AddQueryParameterToURL(file.link, "download", "")),
        });

        internalItems.value.push({
            icon: "file_copy",
            tooltip: t("fileBar.copyUrl"),
            onClick: file => CopyFile(file, UrlType.URL),
        });

        internalItems.value.push({
            icon: "article",
            tooltip: t("fileBar.copyMarkdown"),
            onClick: file => CopyFile(file, UrlType.Markdown),
        });

        if (IsImage(props.file.name)) {
            images.push(props.file);
            internalItems.value.push({
                icon: "visibility",
                tooltip: t("fileBar.view"),
                onClick: () => {
                    imageViewer.value = true;
                },
            });
        }
    }
}

watch(locale, rebuildItems, { immediate: true });
</script>
