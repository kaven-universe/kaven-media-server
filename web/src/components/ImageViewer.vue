<template>
    <q-dialog :model-value="modelValue" @update:model-value="v => $emit('update:modelValue', v)">
        <q-carousel swipeable animated v-model="current" :thumbnails="thumbnails" infinite arrows ref="carousel"
            :style="style" v-model:fullscreen="fullscreen">
            <template v-for="image, index in images" :key="index">
                <q-carousel-slide :name="image.link" :img-src="image.link" />
            </template>

            <template v-slot:control>
                <q-carousel-control position="top-right" :offset="[18, 18]">
                    <q-btn square icon="close" flat @click="$emit('update:modelValue', false)" />
                </q-carousel-control>

                <q-carousel-control position="bottom-right" :offset="[18, 18]">
                    <q-btn push round dense color="white" text-color="primary"
                        :icon="fullscreen ? 'fullscreen_exit' : 'fullscreen'" @click="fullscreen = !fullscreen" />
                </q-carousel-control>
            </template>
        </q-carousel>
    </q-dialog>
</template>

<script setup lang="ts">
import { ref, computed } from "vue";
import { ArrayFirst } from "kaven-basic";
import type { IFile } from "src/common";

const props = defineProps<{
    modelValue: boolean;
    images: IFile[];
    thumbnails?: boolean;
}>();

const currentValue = ref("");
const fullscreen = ref(false);

const current = computed<string | undefined>({
    get() {
        if (!currentValue.value && props.images.length > 0) {
            return ArrayFirst(props.images)?.link;
        }
        return currentValue.value;
    },
    set(val: string | undefined) {
        currentValue.value = val ?? "";
    },
});

const style = computed(() => {
    if (fullscreen.value) {
        return {};
    }
    return { width: "65vw" };
});
</script>
