<template>
    <q-page class="q-pa-md activity-page">
        <div class="activity-content">
            <div class="row items-start justify-between q-col-gutter-md q-mb-md">
                <div>
                    <div class="text-h4">{{ t("activity.title") }}</div>
                    <p class="text-body2 text-grey-7 q-mt-xs q-mb-none">
                        {{ t("activity.description") }}
                    </p>
                </div>
                <q-btn flat round color="primary" icon="refresh" :loading="loading" :aria-label="t('common.refresh')" @click="loadRecords()" />
            </div>

            <q-card flat bordered>
                <q-tabs v-model="kind" align="left" active-color="primary" indicator-color="primary" narrow-indicator>
                    <q-tab name="access" icon="image" :label="t('activity.imageAccesses')" />
                    <q-tab name="download" icon="download" :label="t('activity.hfsDownloads')" />
                </q-tabs>
                <q-separator />

                <q-banner v-if="error" class="bg-red-1 text-negative q-ma-md" rounded>
                    <template #avatar><q-icon name="error" /></template>
                    {{ error }}
                    <template #action><q-btn flat color="negative" :label="t('common.tryAgain')" @click="loadRecords()" /></template>
                </q-banner>

                <q-table
                    v-model:pagination="pagination"
                    flat
                    :rows="records"
                    :columns="columns"
                    row-key="id"
                    :loading="loading"
                    :rows-per-page-options="[10, 25, 50, 100]"
                    binary-state-sort
                    @request="requestRecords"
                >
                    <template #body-cell-resource="props">
                        <q-td :props="props">
                            <div>{{ props.row.resource }}</div>
                        </q-td>
                    </template>
                    <template #body-cell-originalUrl="props">
                        <q-td :props="props">
                            <a
                                v-if="requestLink(props.row.originalUrl)"
                                :href="requestLink(props.row.originalUrl)"
                                target="_blank"
                                rel="noopener noreferrer"
                            >{{ props.row.originalUrl }}</a>
                            <span v-else>{{ props.row.originalUrl }}</span>
                        </q-td>
                    </template>
                    <template #body-cell-userAgent="props">
                        <q-td :props="props">
                            <div>{{ props.row.userAgent || "—" }}</div>
                        </q-td>
                    </template>
                    <template #body-cell-createdAt="props">
                        <q-td :props="props" class="no-wrap">{{ formatDate(props.row.createdAt) }}</q-td>
                    </template>
                    <template #no-data>
                        <div class="full-width row flex-center text-grey-7 q-gutter-sm q-pa-lg">
                            <q-icon name="history" size="sm" />
                            <span>{{ t(kind === "access" ? "activity.emptyAccess" : "activity.emptyDownload") }}</span>
                        </div>
                    </template>
                </q-table>
            </q-card>
        </div>
    </q-page>
</template>

<script setup lang="ts">
import axios from "axios";
import { useAdminSessionStore } from "src/stores/admin-session";
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";

type ActivityKind = "access" | "download";
interface ActivityRecord {
    id: string;
    kind: ActivityKind;
    resource: string;
    imageId?: string;
    ip: string;
    originalUrl: string;
    userAgent?: string;
    createdAt: string;
}
interface ActivityResponse {
    kind: ActivityKind;
    page: number;
    pageSize: number;
    total: number;
    records: ActivityRecord[];
}
interface TableRequest {
    pagination: { page: number; rowsPerPage: number };
}

const adminSession = useAdminSessionStore();
const { t, locale } = useI18n();
const kind = ref<ActivityKind>("access");
const records = ref<ActivityRecord[]>([]);
const loading = ref(false);
const error = ref("");
const pagination = ref({ page: 1, rowsPerPage: 25, rowsNumber: 0, sortBy: "createdAt", descending: true });
const columns = computed(() => {
    const result = [
        { name: "createdAt", label: t("activity.time"), field: "createdAt", align: "left" as const, sortable: false },
        { name: "resource", label: t(kind.value === "access" ? "activity.imageId" : "activity.file"), field: "resource", align: "left" as const, sortable: false },
        { name: "originalUrl", label: t("activity.requestUrl"), field: "originalUrl", align: "left" as const, sortable: false },
        { name: "ip", label: t("activity.ipAddress"), field: "ip", align: "left" as const, sortable: false },
    ];
    if (kind.value === "download") {
        result.push({ name: "userAgent", label: t("activity.userAgent"), field: "userAgent", align: "left" as const, sortable: false });
    }
    return result;
});

onMounted(() => { void loadRecords(); });
watch(kind, () => {
    pagination.value.page = 1;
    void loadRecords();
});

function requestRecords(request: TableRequest) {
    pagination.value.page = request.pagination.page;
    pagination.value.rowsPerPage = request.pagination.rowsPerPage;
    void loadRecords();
}

async function loadRecords() {
    loading.value = true;
    error.value = "";
    try {
        const response = await axios.get<ActivityResponse>("/api/v1/admin/activity", {
            params: { kind: kind.value, page: pagination.value.page, pageSize: pagination.value.rowsPerPage },
        });
        records.value = response.data.records ?? [];
        pagination.value.page = response.data.page;
        pagination.value.rowsPerPage = response.data.pageSize;
        pagination.value.rowsNumber = response.data.total;
    } catch (requestError) {
        records.value = [];
        const status = axios.isAxiosError(requestError) ? requestError.response?.status : undefined;
        if (status === 401 || status === 404) adminSession.clear();
        error.value = axios.isAxiosError(requestError)
            ? (requestError.response?.data as { error?: string } | undefined)?.error ?? requestError.message
            : t("activity.loadFailed");
    } finally {
        loading.value = false;
    }
}

function formatDate(value: string) {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString(locale.value);
}

function requestLink(value: string) {
    try {
        const url = new URL(value, window.location.origin);
        if (url.origin === window.location.origin && (url.protocol === "http:" || url.protocol === "https:")) {
            return `${url.pathname}${url.search}${url.hash}`;
        }
    } catch {
        // Invalid or unsafe recorded values remain visible as plain text.
    }
    return "";
}
</script>

<style scoped>
.activity-page {
    width: 100%;
}

.activity-content {
    width: 100%;
    min-width: 0;
}
</style>
