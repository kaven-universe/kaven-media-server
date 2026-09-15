<template>
    <q-page class="q-pa-md settings-page">
        <div class="settings-content">
            <div class="text-h4">Settings</div>
            <p class="text-body2 text-grey-7 q-mt-xs q-mb-md">
                Manage this server's administrative settings.
            </p>

            <q-card flat bordered class="q-mb-md">
                <q-card-section>
                    <div class="text-h5">Application settings</div>
                    <p class="text-body2 text-grey-7 q-mb-none">
                        These settings are stored in the database. Saving restarts the server briefly.
                    </p>
                </q-card-section>

                <q-separator />

                <q-card-section>
                    <q-form class="q-gutter-md" @submit.prevent="saveSettings">
                        <q-toggle v-model="settings.publicUploads" label="Allow image uploads without administrator login" :disable="settingsBusy" />
                        <div class="settings-limit-grid">
                            <q-input v-model.trim="settings.uploadDirectory" outlined label="Upload directory" hint="Relative to the data directory, for example upload" :disable="settingsBusy" />
                            <q-input v-model.trim="settings.downloadDirectory" outlined label="Download directory" hint="Relative to the data directory; Bing uses its bing subdirectory" :disable="settingsBusy" />
                        </div>
                        <div class="settings-limit-grid">
                            <div>
                                <q-input v-model.number="settings.maxFileCount" outlined type="number" label="Files per upload" min="1" max="1000" :disable="settingsBusy" />
                            </div>
                            <div>
                                <q-input v-model.number="maxImageMiB" outlined type="number" label="Maximum image" suffix="MiB" min="1" max="1024" :disable="settingsBusy" />
                            </div>
                            <div>
                                <q-input v-model.number="maxHFSMiB" outlined type="number" label="Maximum HFS file" suffix="MiB" min="1" max="1048576" :disable="settingsBusy" />
                            </div>
                        </div>
                        <q-input v-model.number="settings.rememberDurationDays" outlined type="number" label="Keep remembered login for" suffix="days" min="1" max="365" :disable="settingsBusy" />
                        <div class="text-subtitle1">Bing wallpaper archive</div>
                        <div class="row items-center q-col-gutter-md">
                            <div class="col-12 col-sm-auto">
                                <q-toggle v-model="settings.bingSyncEnabled" label="Automatically download Bing wallpapers" :disable="settingsBusy" />
                            </div>
                            <div class="col-12 col-sm">
                                <q-input
                                    v-model.number="settings.bingSyncIntervalHours"
                                    outlined
                                    type="number"
                                    label="Synchronization interval"
                                    suffix="hours"
                                    min="1"
                                    max="8760"
                                    :disable="settingsBusy || !settings.bingSyncEnabled"
                                />
                            </div>
                            <div class="col-12 col-sm-auto">
                                <q-btn
                                    outline
                                    color="primary"
                                    icon="download"
                                    label="Synchronize now"
                                    :loading="bingSyncing"
                                    :disable="settingsBusy"
                                    @click="syncBingNow"
                                />
                            </div>
                        </div>
                        <div class="text-caption text-grey-7">
                            Automatic synchronization starts after the configured interval. Manual synchronization works even when automatic downloads are disabled.
                        </div>
                        <q-input v-model="allowedDomainsText" outlined type="textarea" autogrow label="Allowed image referer domains" hint="One hostname per line. Leave empty to allow all referers." :disable="settingsBusy" />
                        <div class="text-subtitle1">HFS roots</div>
                        <div v-for="(root, index) in settings.hfsRoots" :key="index" class="hfs-root-grid">
                            <div class="hfs-root-name">
                                <q-input v-model.trim="root.name" outlined label="Name" :disable="settingsBusy" />
                            </div>
                            <div class="hfs-root-path">
                                <q-input v-model.trim="root.path" outlined label="Path" hint="Relative to the data directory, or an absolute server path" :disable="settingsBusy" />
                            </div>
                            <div class="hfs-root-action"><q-toggle v-model="root.public" label="Public" :disable="settingsBusy" /></div>
                            <div class="hfs-root-action"><q-toggle v-model="root.readOnly" label="Read only" :disable="settingsBusy" /></div>
                            <div class="hfs-root-action"><q-btn flat round icon="delete" color="negative" :disable="settingsBusy" @click="settings.hfsRoots.splice(index, 1)" /></div>
                        </div>
                        <q-btn flat color="primary" icon="add" label="Add HFS root" :disable="settingsBusy || settings.hfsRoots.length >= 64" @click="addHFSRoot" />
                        <div><q-btn color="primary" label="Save application settings" type="submit" :loading="settingsSaving" :disable="settingsLoading" /></div>
                    </q-form>
                </q-card-section>
            </q-card>

            <q-card flat bordered class="q-mb-md">
                <q-card-section>
                    <div class="text-h5">Administrator password</div>
                    <p class="text-body2 text-grey-7 q-mb-none">
                        Change the password used to sign in to this server. Existing sessions
                        are revoked after the password changes.
                    </p>
                </q-card-section>

                <q-separator />

                <q-card-section>
                    <q-banner v-if="passwordManagedExternally" rounded class="bg-blue-1 text-primary">
                        This password is managed by the deployment environment. Update the
                        configured password or secret file and restart the server.
                    </q-banner>

                    <q-form v-else class="q-gutter-md" @submit.prevent="changePassword">
                        <q-input
                            v-model="currentPassword"
                            outlined
                            type="password"
                            label="Current password"
                            autocomplete="current-password"
                            :disable="passwordUpdating || passwordStatusLoading"
                            :rules="[requiredPassword]"
                        />
                        <q-input
                            v-model="newPassword"
                            outlined
                            type="password"
                            label="New password"
                            autocomplete="new-password"
                            hint="Use at least 16 characters."
                            :disable="passwordUpdating || passwordStatusLoading"
                            :rules="newPasswordRules"
                        />
                        <q-input
                            v-model="passwordConfirmation"
                            outlined
                            type="password"
                            label="Confirm new password"
                            autocomplete="new-password"
                            :disable="passwordUpdating || passwordStatusLoading"
                            :rules="passwordConfirmationRules"
                        />
                        <q-btn
                            color="primary"
                            label="Change password"
                            type="submit"
                            :loading="passwordUpdating"
                            :disable="passwordStatusLoading"
                        />
                    </q-form>
                </q-card-section>
            </q-card>

            <q-card flat bordered class="q-mb-md">
                <q-card-section class="row items-start justify-between q-gutter-md">
                    <div>
                        <div class="text-h5">Storage integrity</div>
                        <p class="text-body2 text-grey-7 q-mb-none">
                            Check the current database against files under <code>/data</code>. This is read only and does not depend on a backup.
                        </p>
                    </div>
                    <q-btn
                        color="primary"
                        icon="fact_check"
                        label="Check files"
                        :loading="integrityChecking"
                        @click="checkStorageIntegrity"
                    />
                </q-card-section>

                <q-separator />

                <q-card-section v-if="integrityChecking" class="row items-center q-gutter-md" aria-live="polite">
                    <q-spinner color="primary" size="32px" />
                    <div>
                        <div class="text-subtitle1 text-weight-medium">Checking database records and managed files…</div>
                        <div class="text-body2 text-grey-7">Large image libraries may take a while because image size and checksum are verified.</div>
                    </div>
                </q-card-section>

                <q-card-section v-else-if="integrityError">
                    <q-banner rounded class="bg-red-1 text-negative" aria-live="assertive">
                        <template #avatar><q-icon name="error" /></template>
                        {{ integrityError }}
                    </q-banner>
                </q-card-section>

                <template v-else-if="integrityReport">
                    <q-card-section>
                        <q-banner
                            rounded
                            :class="integrityReport.healthy ? 'bg-green-1 text-positive' : 'bg-orange-1 text-orange-10'"
                            aria-live="polite"
                        >
                            <template #avatar>
                                <q-icon :name="integrityReport.healthy ? 'check_circle' : 'warning'" />
                            </template>
                            <div class="text-weight-medium">
                                {{ integrityReport.healthy
                                    ? "All database file records point to valid files."
                                    : `${missingFileCount} referenced file(s) are missing; ${integrityReport.issueCount} total issue(s) found.` }}
                            </div>
                            <div class="text-body2">
                                {{ integrityReport.referencesChecked.toLocaleString() }} database references and
                                {{ integrityReport.filesChecked.toLocaleString() }} managed files checked.
                            </div>
                        </q-banner>
                    </q-card-section>

                    <q-list v-if="integrityReport.issues.length" bordered separator class="q-mx-md q-mb-md rounded-borders">
                        <q-item v-for="(issue, index) in integrityReport.issues" :key="`${issue.kind}-${issue.path}-${index}`">
                            <q-item-section avatar><q-icon name="report_problem" color="warning" /></q-item-section>
                            <q-item-section>
                                <q-item-label>{{ issue.path || formatIssueKind(issue.kind) }}</q-item-label>
                                <q-item-label caption>{{ formatIssueKind(issue.kind) }} · {{ issue.detail }}</q-item-label>
                            </q-item-section>
                        </q-item>
                    </q-list>
                    <div v-if="integrityReport.truncatedIssues" class="text-caption text-grey-7 q-px-md q-pb-md">
                        {{ integrityReport.truncatedIssues.toLocaleString() }} additional issues were omitted from this report.
                    </div>
                </template>
            </q-card>

            <q-card flat bordered>
                <q-card-section>
                    <div class="text-h5">Backups</div>
                    <p class="text-body2 text-grey-7 q-mb-none">
                        Create and restore database backups in the server's <code>data/backup</code>
                        directory. Managed file directories are never copied into a backup.
                    </p>
                </q-card-section>

                <q-separator />

                <q-card-section class="q-gutter-md">
                    <div class="backup-actions-grid">
                        <q-input
                            v-model.trim="newBackupName"
                            outlined
                            label="New backup name"
                            hint="The server restarts and creates a validated SQLite backup while stopped."
                            :disable="backupCreating || restoreBusy"
                            :rules="[validBackupName]"
                        />
                        <q-btn
                            color="primary"
                            icon="backup"
                            label="Backup"
                            :loading="backupCreating"
                            :disable="restoreBusy"
                            @click="createServerBackup"
                        />
                    </div>

                    <q-banner v-if="backupLastError" class="bg-red-1 text-negative" rounded>
                        <template #avatar><q-icon name="error" /></template>
                        The last server backup failed: {{ backupLastError }}
                    </q-banner>

                    <div class="row items-center justify-between">
                        <div>
                            <div class="text-subtitle1 text-weight-medium">Available backups</div>
                            <div class="text-body2 text-grey-7">
                                You can also securely place database-backup folders directly in <code>data/backup/</code>.
                            </div>
                        </div>
                        <q-btn flat round icon="refresh" color="primary" :loading="backupsLoading" @click="loadBackups" />
                    </div>

                    <q-list v-if="serverBackups.length" bordered separator class="rounded-borders">
                        <q-item
                            v-for="item in serverBackups"
                            :key="item.name"
                            v-ripple
                            clickable
                            :active="selectedServerBackup?.name === item.name"
                            active-class="bg-blue-1 text-primary"
                            @click="selectedServerBackup = item"
                        >
                            <q-item-section avatar>
                                <q-radio v-model="selectedBackupName" :val="item.name" color="primary" />
                            </q-item-section>
                            <q-item-section>
                                <q-item-label>{{ item.name }}</q-item-label>
                                <q-item-label caption>
                                    {{ item.files.toLocaleString() }} files · {{ formatBytes(item.bytes) }} · {{ formatDate(item.created) }}
                                </q-item-label>
                            </q-item-section>
                        </q-item>
                    </q-list>
                    <q-banner v-else-if="!backupsLoading" rounded class="bg-grey-2 text-grey-8">
                        No valid backups were found in <code>data/backup/</code>.
                    </q-banner>
                    <q-skeleton v-else type="rect" height="88px" />

                    <q-banner rounded class="bg-orange-1 text-orange-10">
                        <template #avatar><q-icon name="warning" /></template>
                        Restore replaces only SQLite. Existing image, Bing, cache, and HFS directories stay in place.
                    </q-banner>

                    <div class="row justify-end">
                        <q-btn
                            color="negative"
                            icon="restore"
                            label="Restore"
                            :loading="restoreBusy"
                            :disable="!selectedServerBackup || backupCreating"
                            @click="openRestoreDialog"
                        />
                    </div>

                    <div v-if="restoreBusy" class="restore-progress" aria-live="polite">
                        <q-circular-progress indeterminate color="primary" track-color="grey-3" size="48px" :thickness="0.14" />
                        <div>
                            <div class="text-subtitle1 text-weight-medium">{{ restoreStatus.title }}</div>
                            <div class="text-body2 text-grey-7">{{ restoreStatus.detail }}</div>
                        </div>
                    </div>

                    <q-banner v-if="restorePhase === 'failed' || restorePhase === 'restart-timeout'" rounded class="bg-red-1 text-negative" aria-live="assertive">
                        <template #avatar><q-icon name="error" /></template>
                        <div class="text-weight-medium">
                            {{ restorePhase === "restart-timeout" ? "Server restart is taking longer than expected" : "Restore could not be completed" }}
                        </div>
                        <div class="text-body2">{{ restoreError }}</div>
                    </q-banner>
                </q-card-section>
            </q-card>

            <q-dialog v-model="restoreDialog" persistent>
                <q-card class="restore-dialog">
                    <q-card-section>
                        <div class="text-h5">Restore database</div>
                        <div class="text-body2 text-grey-7">
                            Review retained directories and manually configure HFS virtual roots before replacing SQLite.
                        </div>
                    </q-card-section>
                    <q-separator />
                    <q-card-section>
                        <q-stepper v-model="restoreStep" flat animated color="primary">
                            <q-step :name="1" title="Review" icon="storage" :done="restoreStep > 1">
                                <div class="text-subtitle1">{{ selectedServerBackup?.name }}</div>
                                <p class="text-body2 text-grey-7">
                                    Only the database is restored. These server-managed directories are retained without copying:
                                </p>
                                <q-list bordered separator>
                                    <q-item v-for="directory in retainedDirectories" :key="directory.path">
                                        <q-item-section avatar>
                                            <q-spinner v-if="dataDirectoriesLoading" color="primary" size="sm" />
                                            <q-icon
                                                v-else
                                                :name="dataDirectoryExists(directory.path) ? 'check_circle' : 'warning'"
                                                :color="dataDirectoryExists(directory.path) ? 'positive' : 'warning'"
                                            />
                                        </q-item-section>
                                        <q-item-section>
                                            <q-item-label>{{ directory.label }}</q-item-label>
                                            <q-item-label caption><code>/data/{{ directory.path }}</code></q-item-label>
                                        </q-item-section>
                                        <q-item-section side>{{ dataDirectoryExists(directory.path) ? "Detected" : "Not found" }}</q-item-section>
                                    </q-item>
                                </q-list>
                                <div class="row items-center q-mt-sm">
                                    <q-btn flat dense color="primary" icon="refresh" label="Read /data directories" :loading="dataDirectoriesLoading" @click="loadDataDirectories" />
                                    <span v-if="dataDirectoriesError" class="text-negative text-caption q-ml-sm">{{ dataDirectoriesError }}</span>
                                </div>
                                <q-banner rounded class="bg-blue-1 text-blue-10 q-mt-md">
                                    If this database came from another server, copy or mount that server's matching content at these
                                    target paths before restoring. Original-image and Bing paths stored in SQLite are relative to them.
                                </q-banner>
                            </q-step>

                            <q-step :name="2" title="Virtual directories" icon="account_tree" :done="restoreStep > 2">
                                <p class="text-body2 text-grey-7">
                                    These virtual directories were read from the selected backup database. Every mapping uses an
                                    absolute server path. Read only independently controls whether the server exposes upload and
                                    folder-creation operations.
                                </p>
                                <div v-if="backupMappingsLoading" class="row items-center q-gutter-sm q-pa-md">
                                    <q-spinner color="primary" />
                                    <span>Reading virtual directories from the backup database…</span>
                                </div>
                                <q-banner v-else-if="backupMappingsError" rounded class="bg-red-1 text-negative q-mb-md">
                                    {{ backupMappingsError }}
                                    <template #action>
                                        <q-btn flat color="negative" label="Try again" @click="loadBackupMappings(selectedServerBackup?.name ?? '')" />
                                    </template>
                                </q-banner>
                                <template v-else>
                                    <div v-for="(root, index) in restoreSettings.hfsRoots" :key="index" class="hfs-root-grid q-mb-md">
                                        <div class="hfs-root-name"><q-input v-model.trim="root.name" outlined label="Virtual name" /></div>
                                        <div class="hfs-root-path">
                                            <q-select
                                                v-model="root.path"
                                                outlined
                                                use-input
                                                fill-input
                                                hide-selected
                                                new-value-mode="add-unique"
                                                input-debounce="0"
                                                label="Server directory"
                                                hint="Choose a detected directory, or enter a relative or absolute server path"
                                                :options="filteredHFSDirectories"
                                                :loading="dataDirectoriesLoading"
                                                @filter="filterHFSDirectories"
                                            >
                                                <template #prepend><q-icon name="folder" /></template>
                                                <template #option="scope">
                                                    <q-item v-bind="scope.itemProps">
                                                        <q-item-section avatar><q-icon name="folder" /></q-item-section>
                                                        <q-item-section><q-item-label>{{ scope.opt }}</q-item-label></q-item-section>
                                                    </q-item>
                                                </template>
                                            </q-select>
                                        </div>
                                        <div class="hfs-root-action"><q-toggle v-model="root.public" label="Public" /></div>
                                        <div class="hfs-root-action"><q-toggle v-model="root.readOnly" label="Read only" /></div>
                                        <div class="hfs-root-action"><q-btn flat round icon="delete" color="negative" @click="restoreSettings.hfsRoots.splice(index, 1)" /></div>
                                    </div>
                                    <q-btn flat color="primary" icon="add" label="Add HFS virtual root" :disable="restoreSettings.hfsRoots.length >= 64" @click="addRestoreHFSRoot" />
                                </template>
                            </q-step>

                            <q-step :name="3" title="Confirm" icon="restore">
                                <q-banner rounded class="bg-orange-1 text-orange-10">
                                    <template #avatar><q-icon name="warning" /></template>
                                    The selected database will replace the active database. Current administrator access is retained,
                                    other application settings come from the selected database, and no managed directory is deleted
                                    or overwritten.
                                </q-banner>
                            </q-step>

                            <template #navigation>
                                <q-stepper-navigation class="row justify-between">
                                    <div>
                                        <q-btn v-if="restoreStep > 1" flat color="primary" label="Back" @click="restoreStep--" />
                                    </div>
                                    <div class="q-gutter-sm">
                                        <q-btn flat label="Cancel" :disable="restoreBusy" v-close-popup />
                                        <q-btn
                                            v-if="restoreStep < 3"
                                            color="primary"
                                            label="Continue"
                                            :disable="backupMappingsLoading || !!backupMappingsError"
                                            @click="restoreStep++"
                                        />
                                        <q-btn v-else color="negative" icon="restore" label="Restore database" :loading="restoreBusy" @click="restore" />
                                    </div>
                                </q-stepper-navigation>
                            </template>
                        </q-stepper>
                    </q-card-section>
                </q-card>
            </q-dialog>
        </div>
    </q-page>
</template>

<script setup lang="ts">
import axios from "axios";
import { useQuasar } from "quasar";
import { useAdminSessionStore } from "src/stores/admin-session";
import { computed, onMounted, ref } from "vue";

const $q = useQuasar();
const adminSession = useAdminSessionStore();
interface ServerBackup { name: string; files: number; bytes: number; manifestVersion: number; created: string }
interface BackupStatus { backups: ServerBackup[]; lastError?: string }
interface DataDirectoriesResponse { directories: string[] }
interface BackupMappingsResponse { uploadDirectory: string; downloadDirectory: string; hfsRoots: HFSRoot[] }
interface IntegrityIssue { kind: string; path?: string; detail: string }
interface IntegrityReport {
    healthy: boolean;
    referencesChecked: number;
    filesChecked: number;
    checksumsChecked: number;
    checksumBytes: number;
    issueCount: number;
    issues: IntegrityIssue[];
    truncatedIssues: number;
}
const serverBackups = ref<ServerBackup[]>([]);
const selectedServerBackup = ref<ServerBackup>();
const backupsLoading = ref(false);
const backupCreating = ref(false);
const backupLastError = ref("");
const newBackupName = ref(defaultBackupName());
type RestorePhase = "idle" | "validating" | "restarting" | "failed" | "restart-timeout";
const restorePhase = ref<RestorePhase>("idle");
const restoreError = ref("");
const restoreDialog = ref(false);
const restoreStep = ref(1);
const dataDirectories = ref<string[]>([]);
const filteredHFSDirectories = ref<string[]>([]);
const dataDirectoriesLoading = ref(false);
const dataDirectoriesError = ref("");
const backupMappingsLoading = ref(false);
const backupMappingsError = ref("");
const integrityChecking = ref(false);
const integrityError = ref("");
const integrityReport = ref<IntegrityReport>();
const passwordManagedExternally = ref(false);
const adminUsername = ref("admin");
const passwordStatusLoading = ref(true);
const passwordUpdating = ref(false);
const currentPassword = ref("");
const newPassword = ref("");
const passwordConfirmation = ref("");
interface HFSRoot { name: string; path: string; public: boolean; readOnly?: boolean }
interface ApplicationSettings {
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
    hfsRoots: HFSRoot[];
}
const settings = ref<ApplicationSettings>({ uploadDirectory: "upload", downloadDirectory: "download", publicUploads: true, maxFileCount: 100, maxImageFileSize: 100 * 1024 * 1024, maxHFSFileSize: 1000 * 1024 * 1024, rememberDurationDays: 30, bingSyncEnabled: true, bingSyncIntervalHours: 24, allowedDomainNames: [], hfsRoots: [] });
const restoreSettings = ref<ApplicationSettings>({ uploadDirectory: "upload", downloadDirectory: "download", publicUploads: true, maxFileCount: 100, maxImageFileSize: 100 * 1024 * 1024, maxHFSFileSize: 1000 * 1024 * 1024, rememberDurationDays: 30, bingSyncEnabled: true, bingSyncIntervalHours: 24, allowedDomainNames: [], hfsRoots: [] });
const retainedDirectories = computed(() => [
    { label: "Original images", path: restoreSettings.value.uploadDirectory },
    { label: "Generated image cache", path: "cache" },
    { label: "Bing archive", path: `${restoreSettings.value.downloadDirectory}/bing` },
]);
const allowedDomainsText = ref("");
const maxImageMiB = ref(100);
const maxHFSMiB = ref(1000);
const settingsLoading = ref(true);
const settingsSaving = ref(false);
const bingSyncing = ref(false);
const settingsBusy = computed(() => settingsLoading.value || settingsSaving.value);
const selectedBackupName = computed({
    get: () => selectedServerBackup.value?.name ?? "",
    set: (name: string) => { selectedServerBackup.value = serverBackups.value.find(item => item.name === name); },
});
const restoreBusy = computed(() => restorePhase.value === "validating" || restorePhase.value === "restarting");
const missingFileCount = computed(() => integrityReport.value?.issues.filter(issue => issue.kind === "missing_file").length ?? 0);
const restoreStatus = computed(() => {
    switch (restorePhase.value) {
        case "validating":
            return { title: "Validating database backup", detail: "The server is checking the manifest, SQLite checksum, schema, and directory mappings." };
        case "restarting":
            return { title: "Applying restored data", detail: "Validation passed. Waiting for the server to restart with the restored data." };
        default:
            return { title: "Preparing restore", detail: "Preparing the selected server backup." };
    }
});
const requiredPassword = (value: string) => value.length > 0 || "Enter your current password.";
const newPasswordRules = [
    (value: string) => new TextEncoder().encode(value).length >= 16 || "Use at least 16 characters.",
    (value: string) => new TextEncoder().encode(value).length <= 1024 || "Use no more than 1024 bytes.",
    (value: string) => value !== currentPassword.value || "Use a different password.",
];
const passwordConfirmationRules = [
    (value: string) => value === newPassword.value || "Passwords do not match.",
];

onMounted(() => {
    void loadPasswordStatus();
    void loadSettings();
    void loadBackups();
});

async function loadBackups() {
    backupsLoading.value = true;
    try {
        const response = await axios.get<BackupStatus>("/api/v1/admin/backups");
        const selectedName = selectedServerBackup.value?.name;
        serverBackups.value = response.data.backups ?? [];
        selectedServerBackup.value = selectedName
            ? serverBackups.value.find(item => item.name === selectedName)
            : undefined;
        backupLastError.value = response.data.lastError ?? "";
    } catch (error) {
        if (axios.isAxiosError(error) && (error.response?.status === 401 || error.response?.status === 404)) adminSession.clear();
        $q.notify({ type: "negative", message: "Could not load server backups." });
    } finally {
        backupsLoading.value = false;
    }
}

async function checkStorageIntegrity() {
    integrityChecking.value = true;
    integrityError.value = "";
    integrityReport.value = undefined;
    try {
        const response = await axios.post<IntegrityReport>("/api/v1/admin/integrity/check", undefined, { timeout: 0 });
        integrityReport.value = response.data;
    } catch (error) {
        const status = axios.isAxiosError(error) ? error.response?.status : undefined;
        if (status === 401 || status === 404) adminSession.clear();
        integrityError.value = requestError(error, "Could not check storage integrity.");
    } finally {
        integrityChecking.value = false;
    }
}

function formatIssueKind(kind: string) {
    return kind.split("_").map(word => word.charAt(0).toUpperCase() + word.slice(1)).join(" ");
}

async function createServerBackup() {
    const validation = validBackupName(newBackupName.value);
    if (validation !== true) {
        $q.notify({ type: "negative", message: validation });
        return;
    }
    backupCreating.value = true;
    try {
        await axios.post("/api/v1/admin/backups", { name: newBackupName.value });
        $q.notify({ type: "positive", message: "Backup queued. The server is restarting to create it…", timeout: 0 });
        await waitForBackup(newBackupName.value, 1800);
        window.location.reload();
    } catch (error) {
        const status = axios.isAxiosError(error) ? error.response?.status : undefined;
        if (status === 401 || status === 404) adminSession.clear();
        const message = axios.isAxiosError(error)
            ? (error.response?.data as { error?: string } | undefined)?.error ?? error.message
            : "Could not create backup.";
        $q.notify({ type: "negative", message, timeout: 0, actions: [{ icon: "close", color: "white" }] });
        backupCreating.value = false;
    }
}

async function waitForBackup(name: string, attempts: number) {
    for (let attempt = 0; attempt < attempts; attempt++) {
        await new Promise(resolve => window.setTimeout(resolve, 1000));
        try {
            const response = await axios.get<BackupStatus>("/api/v1/admin/backups", { timeout: 2000 });
            if (response.data.backups?.some(item => item.name === name)) return;
            if (response.data.lastError) throw new Error(response.data.lastError);
        } catch (error) {
            if (!axios.isAxiosError(error)) throw error;
            if (error.response?.status === 401 || error.response?.status === 404) {
                adminSession.clear();
                throw error;
            }
        }
    }
    throw new Error("Backup creation did not finish before the waiting period expired.");
}

function validBackupName(value: string) {
    return (/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(value) && !value.endsWith("."))
        || "Use 1-128 letters, numbers, dots, underscores, or hyphens.";
}

function defaultBackupName() {
    const now = new Date();
    const stamp = [now.getFullYear(), now.getMonth() + 1, now.getDate(), now.getHours(), now.getMinutes(), now.getSeconds()]
        .map(value => value.toString().padStart(2, "0")).join("");
    return `backup-${stamp}`;
}

async function loadSettings() {
    settingsLoading.value = true;
    try {
        const response = await axios.get<ApplicationSettings>("/api/v1/admin/settings");
        settings.value = response.data;
        allowedDomainsText.value = response.data.allowedDomainNames.join("\n");
        maxImageMiB.value = response.data.maxImageFileSize / 1024 / 1024;
        maxHFSMiB.value = response.data.maxHFSFileSize / 1024 / 1024;
    } catch (error) {
        if (axios.isAxiosError(error) && (error.response?.status === 401 || error.response?.status === 404)) adminSession.clear();
        $q.notify({ type: "negative", message: "Could not load application settings." });
    } finally {
        settingsLoading.value = false;
    }
}

function addHFSRoot() {
    settings.value.hfsRoots.push({ name: "", path: "", public: false, readOnly: false });
}

function addRestoreHFSRoot() {
    restoreSettings.value.hfsRoots.push({ name: "", path: "", public: false, readOnly: false });
}

function dataDirectoryExists(directory: string) {
    const suffix = `/${directory}`;
    return dataDirectories.value.some(value => {
        const normalized = value.replaceAll("\\", "/");
        return normalized === directory || normalized.endsWith(suffix);
    });
}

function availableHFSDirectories() {
    return dataDirectories.value;
}

function filterHFSDirectories(value: string, update: (callback: () => void) => void) {
    update(() => {
        const needle = value.trim().toLowerCase();
        filteredHFSDirectories.value = availableHFSDirectories()
            .filter(directory => !needle || directory.toLowerCase().includes(needle));
    });
}

function requestError(error: unknown, fallback: string) {
    return axios.isAxiosError(error)
        ? (error.response?.data as { error?: string } | undefined)?.error ?? error.message
        : fallback;
}

async function loadDataDirectories() {
    dataDirectoriesLoading.value = true;
    dataDirectoriesError.value = "";
    try {
        const response = await axios.get<DataDirectoriesResponse>("/api/v1/admin/data-directories");
        dataDirectories.value = response.data.directories ?? [];
        filteredHFSDirectories.value = availableHFSDirectories();
    } catch (error) {
        dataDirectories.value = [];
        filteredHFSDirectories.value = [];
        dataDirectoriesError.value = requestError(error, "Could not read /data directories.");
    } finally {
        dataDirectoriesLoading.value = false;
    }
}

async function loadBackupMappings(name: string) {
    if (!name) return;
    backupMappingsLoading.value = true;
    backupMappingsError.value = "";
    try {
        const response = await axios.get<BackupMappingsResponse>(
            `/api/v1/admin/backups/${encodeURIComponent(name)}/mappings`,
        );
        restoreSettings.value.uploadDirectory = response.data.uploadDirectory;
        restoreSettings.value.downloadDirectory = response.data.downloadDirectory;
        restoreSettings.value.hfsRoots = response.data.hfsRoots ?? [];
    } catch (error) {
        restoreSettings.value.hfsRoots = [];
        backupMappingsError.value = requestError(error, "Could not read virtual directories from the backup.");
    } finally {
        backupMappingsLoading.value = false;
    }
}

function openRestoreDialog() {
    if (!selectedServerBackup.value) return;
    restoreSettings.value = JSON.parse(JSON.stringify(settings.value)) as ApplicationSettings;
    restoreStep.value = 1;
    restoreError.value = "";
    backupMappingsError.value = "";
    restoreDialog.value = true;
    void loadDataDirectories();
    void loadBackupMappings(selectedServerBackup.value.name);
}

async function saveSettings() {
    settingsSaving.value = true;
    try {
        settings.value.allowedDomainNames = allowedDomainsText.value.split(/\r?\n|,/).map(value => value.trim()).filter(Boolean);
        settings.value.maxImageFileSize = Math.round(maxImageMiB.value * 1024 * 1024);
        settings.value.maxHFSFileSize = Math.round(maxHFSMiB.value * 1024 * 1024);
        await axios.put("/api/v1/admin/settings", settings.value);
        $q.notify({ type: "positive", message: "Settings saved. Restarting server…" });
        await waitForRestart("Settings update", 30);
        window.location.reload();
    } catch (error) {
        const message = axios.isAxiosError(error)
            ? (error.response?.data as { error?: string } | undefined)?.error ?? error.message
            : "Could not update application settings.";
        $q.notify({ type: "negative", message });
    } finally {
        settingsSaving.value = false;
    }
}

async function syncBingNow() {
    bingSyncing.value = true;
    try {
        await axios.post("/api/v1/admin/bing/sync");
        $q.notify({ type: "positive", message: "Bing wallpaper synchronization queued." });
    } catch (error) {
        const status = axios.isAxiosError(error) ? error.response?.status : undefined;
        if (status === 401 || status === 404) adminSession.clear();
        $q.notify({ type: "negative", message: requestError(error, "Could not start Bing wallpaper synchronization.") });
    } finally {
        bingSyncing.value = false;
    }
}

async function loadPasswordStatus() {
    passwordStatusLoading.value = true;
    try {
        const response = await axios.get("/api/v1/admin/password");
        passwordManagedExternally.value = response.data?.managedExternally === true;
        if (typeof response.data?.username === "string" && response.data.username.length > 0) {
            adminUsername.value = response.data.username;
        }
    } catch (error) {
        if (axios.isAxiosError(error) && (error.response?.status === 401 || error.response?.status === 404)) {
            adminSession.clear();
        }
        $q.notify({ type: "negative", message: "Could not load password settings." });
    } finally {
        passwordStatusLoading.value = false;
    }
}

async function changePassword() {
    passwordUpdating.value = true;
    const replacement = newPassword.value;
    try {
        await axios.post("/api/v1/admin/password", {
            currentPassword: currentPassword.value,
            newPassword: replacement,
        });
        adminSession.clear();
        await waitForRestart("Password change", 30);
        await adminSession.login(adminUsername.value, replacement);
        currentPassword.value = "";
        newPassword.value = "";
        passwordConfirmation.value = "";
        $q.notify({ type: "positive", message: "Administrator password changed." });
    } catch (error) {
        const message = axios.isAxiosError(error)
            ? (error.response?.data as { error?: string } | undefined)?.error ?? error.message
            : "Password change failed";
        $q.notify({ type: "negative", message });
    } finally {
        passwordUpdating.value = false;
    }
}

async function restore() {
    restoreError.value = "";
    restorePhase.value = "validating";
    try {
        if (!selectedServerBackup.value) throw new Error("Select a server backup.");
        await axios.post("/api/v1/admin/backups/restore", {
            name: selectedServerBackup.value.name,
            hfsRoots: restoreSettings.value.hfsRoots,
        }, { timeout: 0 });
        restorePhase.value = "restarting";
        $q.notify({ type: "positive", message: "Backup validated. Applying restored data…", timeout: 0 });
        await waitForRestart("Restore", 120);
        restoreDialog.value = false;
        window.location.hash = "#/";
        window.location.reload();
    } catch (error) {
        const status = axios.isAxiosError(error) ? error.response?.status : undefined;
        if (status === 401 || status === 404) {
            adminSession.clear();
        }
        const message = axios.isAxiosError(error)
            ? (error.response?.data as { error?: string } | undefined)?.error ?? error.message
            : "Restore failed";
        const restartTimedOut = restorePhase.value === "restarting";
        restoreError.value = restartTimedOut
            ? "The backup was accepted. Check that the server is running, then reload this page. Do not submit the restore again."
            : message;
        restorePhase.value = restartTimedOut ? "restart-timeout" : "failed";
        $q.notify({ type: "negative", message, timeout: 0, actions: [{ icon: "close", color: "white" }] });
    }
}

async function waitForRestart(operation: string, attempts: number) {
    let observedOffline = false;
    for (let attempt = 0; attempt < attempts; attempt++) {
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
    throw new Error(`${operation} was accepted, but the server did not return.`);
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

function formatDate(value: string) {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}
</script>

<style scoped>
.settings-page {
    display: flex;
    justify-content: center;
}

.settings-content {
    width: 100%;
    max-width: 760px;
}

.restore-dialog {
    width: min(900px, 95vw);
    max-width: 900px;
}

.settings-limit-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 16px;
}

.hfs-root-grid {
    display: grid;
    grid-template-columns: minmax(140px, 0.7fr) minmax(220px, 1.2fr) auto auto auto;
    gap: 8px;
    align-items: start;
}

.hfs-root-action {
    padding-top: 8px;
}

.backup-actions-grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 16px;
    align-items: center;
}

.restore-progress {
    display: flex;
    align-items: center;
    gap: 20px;
    min-height: 96px;
}

@media (max-width: 599px) {
    .settings-limit-grid,
    .hfs-root-grid,
    .backup-actions-grid {
        grid-template-columns: 1fr;
    }

    .hfs-root-action {
        padding-top: 0;
    }
}
</style>
