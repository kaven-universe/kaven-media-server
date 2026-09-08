<template>
    <q-layout view="lHh Lpr lFf">
        <q-header elevated>
            <q-toolbar>
                <q-toolbar-title>
                    Kaven Media Server
                </q-toolbar-title>

                <q-space />

                <q-btn-toggle v-model="tab" flat stretch toggle-color="yellow" :options="options" />
            </q-toolbar>
        </q-header>

        <q-page-container>
            <router-view />
        </q-page-container>
    </q-layout>
</template>

<script setup lang="ts">
import { ConvertTo } from "kaven-basic";
import { GetRouteByName, Logger, RouteName } from "src/common";
import { ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

const route = useRoute();
const router = useRouter();

const tab = ref<RouteName>(
    ConvertTo(route.name?.toString() ?? RouteName.Upload),
);

const options = Object.keys(RouteName).map(p => ({
    label: p,
    value: GetRouteByName(p),
}));

watch(tab, (newTab) => {
    switch (newTab) {
        case RouteName.Upload:
            router.push("/").catch(ex => Logger.Error(ex));
            break;
        case RouteName.HFS:
            router.push("/hfs").catch(ex => Logger.Error(ex));
            break;
        case RouteName.Restore:
            router.push("/restore").catch(ex => Logger.Error(ex));
            break;
    }
});
</script>
