<template>
    <q-page class="q-pa-md settings-page">
        <div class="settings-shell">
            <aside class="settings-sidebar" :aria-label="t('settings.section')">
                <q-card flat bordered>
                    <q-list padding>
                        <q-item
                            v-for="section in settingsSections"
                            :key="section.value"
                            v-ripple
                            clickable
                            :active="activeSettingsSection === section.value"
                            active-class="bg-blue-1 text-primary"
                            @click="activeSettingsSection = section.value"
                        >
                            <q-item-section avatar><q-icon :name="section.icon" /></q-item-section>
                            <q-item-section>
                                <q-item-label>{{ section.label }}</q-item-label>
                                <q-item-label caption>{{ section.caption }}</q-item-label>
                            </q-item-section>
                        </q-item>
                    </q-list>
                </q-card>
            </aside>

            <main class="settings-content">
                <div class="text-h4">{{ t("settings.title") }}</div>
                <p class="text-body2 text-grey-7 q-mt-xs q-mb-md">
                    {{ t("settings.description") }}
                </p>

                <q-select
                    v-model="activeSettingsSection"
                    class="settings-mobile-navigation q-mb-md"
                    outlined
                    emit-value
                    map-options
                    behavior="menu"
                    :label="t('settings.section')"
                    :options="settingsSections"
                    option-value="value"
                    option-label="label"
                >
                    <template #prepend><q-icon :name="activeSection.icon" /></template>
                    <template #option="scope">
                        <q-item v-bind="scope.itemProps">
                            <q-item-section avatar><q-icon :name="scope.opt.icon" /></q-item-section>
                            <q-item-section>
                                <q-item-label>{{ scope.opt.label }}</q-item-label>
                                <q-item-label caption>{{ scope.opt.caption }}</q-item-label>
                            </q-item-section>
                        </q-item>
                    </template>
                </q-select>

            <q-card v-show="activeSettingsSection === 'general'" flat bordered>
                <q-card-section>
                    <div class="text-h5">{{ t("settings.generalTitle") }}</div>
                    <p class="text-body2 text-grey-7 q-mb-none">
                        {{ t("settings.generalDescription") }}
                    </p>
                </q-card-section>

                <q-separator />

                <q-card-section>
                    <q-form class="q-gutter-md" @submit.prevent="saveSettings">
                        <q-toggle v-model="settings.publicUploads" :label="t('fields.publicUploads')" :disable="settingsBusy" />
                        <div class="settings-limit-grid">
                            <q-input v-model.trim="settings.uploadDirectory" outlined :label="t('fields.uploadDirectory')" :hint="t('settings.uploadHint')" :disable="settingsBusy" />
                            <q-input v-model.trim="settings.downloadDirectory" outlined :label="t('fields.downloadDirectory')" :hint="t('settings.downloadHint')" :disable="settingsBusy" />
                        </div>
                        <div class="settings-limit-grid">
                            <div>
                                <q-input v-model.number="settings.maxFileCount" outlined type="number" :label="t('fields.filesPerUpload')" min="1" max="1000" :disable="settingsBusy" />
                            </div>
                            <div>
                                <q-input v-model.number="maxImageMiB" outlined type="number" :label="t('fields.maximumImage')" suffix="MiB" min="1" max="1024" :disable="settingsBusy" />
                            </div>
                            <div>
                                <q-input v-model.number="maxHFSMiB" outlined type="number" :label="t('fields.maximumHfsFile')" suffix="MiB" min="1" max="1048576" :disable="settingsBusy" />
                            </div>
                        </div>
                        <q-input v-model.number="settings.rememberDurationDays" outlined type="number" :label="t('fields.rememberLogin')" :suffix="t('common.days')" min="1" max="365" :disable="settingsBusy" />
                        <q-input v-model="allowedDomainsText" outlined type="textarea" autogrow :label="t('fields.allowedReferers')" :hint="t('fields.refererHint')" :disable="settingsBusy" />
                        <q-input v-model="trustedProxyCIDRsText" outlined type="textarea" autogrow :label="t('fields.trustedProxyCIDRs')" :hint="t('fields.trustedProxyHint')" :disable="settingsBusy" />
                        <div><q-btn color="primary" :label="t('settings.saveGeneral')" type="submit" :loading="settingsSaving" :disable="settingsLoading" /></div>
                    </q-form>
                </q-card-section>
            </q-card>

            <q-card v-show="activeSettingsSection === 'bing'" flat bordered>
                <q-card-section>
                    <div class="text-h5">{{ t("fields.bingArchive") }}</div>
                    <p class="text-body2 text-grey-7 q-mb-none">
                        {{ t("settings.bingDescription") }}
                    </p>
                </q-card-section>
                <q-separator />
                <q-card-section>
                    <q-form class="q-gutter-md" @submit.prevent="saveSettings">
                        <q-toggle v-model="settings.bingSyncEnabled" :label="t('fields.automaticBing')" :disable="settingsBusy" />
                        <q-input
                            v-model.number="settings.bingSyncIntervalHours"
                            outlined
                            type="number"
                            :label="t('fields.syncInterval')"
                            :suffix="t('common.hours')"
                            min="1"
                            max="8760"
                            :disable="settingsBusy || !settings.bingSyncEnabled"
                        />
                        <div class="text-caption text-grey-7">
                            {{ t("settings.bingHelp") }}
                        </div>
                        <div class="row q-gutter-sm">
                            <q-btn color="primary" :label="t('settings.saveBing')" type="submit" :loading="settingsSaving" :disable="settingsLoading" />
                            <q-btn outline color="primary" icon="download" :label="t('settings.syncNow')" :loading="bingSyncing" :disable="settingsBusy" @click="syncBingNow" />
                        </div>
                    </q-form>
                </q-card-section>
            </q-card>

            <q-card v-show="activeSettingsSection === 'hfs'" flat bordered>
                <q-card-section>
                    <div class="text-h5">{{ t("fields.hfsRoots") }}</div>
                    <p class="text-body2 text-grey-7 q-mb-none">
                        {{ t("settings.hfsDescription") }}
                    </p>
                </q-card-section>
                <q-separator />
                <q-card-section>
                    <q-form class="q-gutter-md" @submit.prevent="saveSettings">
                        <div v-for="(root, index) in settings.hfsRoots" :key="index" class="hfs-root-grid">
                            <div class="hfs-root-name">
                                <q-input v-model.trim="root.name" outlined :label="t('common.name')" :disable="settingsBusy" />
                            </div>
                            <div class="hfs-root-path">
                                <q-input v-model.trim="root.path" outlined :label="t('common.path')" :hint="t('fields.hfsPathHint')" :disable="settingsBusy" />
                            </div>
                            <div class="hfs-root-action"><q-toggle v-model="root.public" :label="t('common.public')" :disable="settingsBusy" /></div>
                            <div class="hfs-root-action"><q-toggle v-model="root.readOnly" :label="t('common.readOnly')" :disable="settingsBusy" /></div>
                            <div class="hfs-root-action"><q-btn flat round icon="delete" color="negative" :disable="settingsBusy" @click="settings.hfsRoots.splice(index, 1)" /></div>
                        </div>
                        <q-btn flat color="primary" icon="add" :label="t('fields.addHfsRoot')" :disable="settingsBusy || settings.hfsRoots.length >= 64" @click="addHFSRoot" />
                        <div><q-btn color="primary" :label="t('settings.saveHfs')" type="submit" :loading="settingsSaving" :disable="settingsLoading" /></div>
                    </q-form>
                </q-card-section>
            </q-card>

            <q-card v-show="activeSettingsSection === 'logging'" flat bordered>
                <q-card-section>
                    <div class="text-h5">{{ t("settings.loggingTitle") }}</div>
                    <p class="text-body2 text-grey-7 q-mb-none">
                        {{ t("settings.loggingDescription") }}
                    </p>
                </q-card-section>
                <q-separator />
                <q-card-section>
                    <q-form class="q-gutter-md" @submit.prevent="saveSettings">
                        <q-toggle v-model="settings.localLogEnabled" :label="t('settings.localLogEnabled')" :disable="settingsBusy" />
                        <div class="settings-limit-grid">
                            <q-input
                                v-model.number="logMaxMiB"
                                outlined
                                type="number"
                                :label="t('settings.logMaxFileSize')"
                                suffix="MiB"
                                min="1"
                                max="1024"
                                :disable="settingsBusy || !settings.localLogEnabled"
                            />
                            <q-input
                                v-model.number="settings.logMaxBackups"
                                outlined
                                type="number"
                                :label="t('settings.logMaxBackups')"
                                :hint="t('settings.logMaxBackupsHint')"
                                min="0"
                                max="100"
                                :disable="settingsBusy || !settings.localLogEnabled"
                            />
                        </div>
                        <div class="text-caption text-grey-7">{{ t("settings.loggingPath") }}</div>
                        <div><q-btn color="primary" :label="t('settings.saveLogging')" type="submit" :loading="settingsSaving" :disable="settingsLoading" /></div>
                    </q-form>
                </q-card-section>
            </q-card>

            <q-card v-show="activeSettingsSection === 'version'" flat bordered>
                <q-card-section class="row items-start justify-between q-gutter-md">
                    <div>
                        <div class="text-h5">{{ t("settings.versionTitle") }}</div>
                        <p class="text-body2 text-grey-7 q-mb-none">
                            {{ t("settings.versionDescription") }}
                        </p>
                    </div>
                    <q-btn
                        color="primary"
                        icon="system_update_alt"
                        :label="t('settings.checkVersion')"
                        :loading="versionChecking"
                        :disable="versionLoading"
                        @click="loadVersion(true)"
                    />
                </q-card-section>

                <q-separator />

                <q-card-section v-if="versionLoading" class="row items-center q-gutter-md">
                    <q-spinner color="primary" size="32px" />
                    <span>{{ t("settings.loadingVersion") }}</span>
                </q-card-section>
                <q-card-section v-else-if="versionStatus" class="q-gutter-md">
                    <q-list bordered separator>
                        <q-item>
                            <q-item-section>
                                <q-item-label caption>{{ t("settings.currentVersion") }}</q-item-label>
                                <q-item-label class="text-h6">
                                    {{ versionStatus.current.version }}
                                    <q-chip v-if="versionStatus.current.modified" dense color="warning" text-color="dark">
                                        {{ t("settings.modifiedBuild") }}
                                    </q-chip>
                                </q-item-label>
                            </q-item-section>
                        </q-item>
                        <q-item>
                            <q-item-section>
                                <q-item-label caption>{{ t("settings.revision") }}</q-item-label>
                                <q-item-label class="text-mono">{{ versionStatus.current.revision }}</q-item-label>
                            </q-item-section>
                        </q-item>
                    </q-list>

                    <q-banner v-if="versionStatus.latest" rounded :class="versionBannerClass" aria-live="polite">
                        <template #avatar><q-icon :name="versionBannerIcon" /></template>
                        <div class="text-weight-medium">{{ versionResultMessage }}</div>
                        <div class="text-body2">
                            {{ t("settings.latestVersion", { version: versionStatus.latest.version }) }}
                        </div>
                        <template #action>
                            <q-btn
                                flat
                                :href="versionStatus.latest.url"
                                target="_blank"
                                rel="noopener noreferrer"
                                :label="t('settings.viewRelease')"
                            />
                        </template>
                    </q-banner>

                    <q-banner v-if="versionError" rounded class="bg-red-1 text-negative" aria-live="assertive">
                        <template #avatar><q-icon name="error" /></template>
                        {{ versionError }}
                    </q-banner>
                </q-card-section>
                <q-card-section v-else-if="versionError">
                    <q-banner rounded class="bg-red-1 text-negative" aria-live="assertive">
                        <template #avatar><q-icon name="error" /></template>
                        {{ versionError }}
                    </q-banner>
                </q-card-section>
            </q-card>

            <q-card v-show="activeSettingsSection === 'security'" flat bordered>
                <q-card-section>
                    <div class="text-h5">{{ t("settings.passwordTitle") }}</div>
                    <p class="text-body2 text-grey-7 q-mb-none">
                        {{ t("settings.passwordDescription") }}
                    </p>
                </q-card-section>

                <q-separator />

                <q-card-section>
                    <q-banner v-if="passwordManagedExternally" rounded class="bg-blue-1 text-primary">
                        {{ t("settings.externallyManaged") }}
                    </q-banner>

                    <q-form v-else class="q-gutter-md" @submit.prevent="changePassword">
                        <q-input
                            v-model="currentPassword"
                            outlined
                            type="password"
                            :label="t('settings.currentPassword')"
                            autocomplete="current-password"
                            :disable="passwordUpdating || passwordStatusLoading"
                            :rules="[requiredPassword]"
                        />
                        <q-input
                            v-model="newPassword"
                            outlined
                            type="password"
                            :label="t('settings.newPassword')"
                            autocomplete="new-password"
                            :hint="t('setup.passwordHint')"
                            :disable="passwordUpdating || passwordStatusLoading"
                            :rules="newPasswordRules"
                        />
                        <q-input
                            v-model="passwordConfirmation"
                            outlined
                            type="password"
                            :label="t('settings.confirmNewPassword')"
                            autocomplete="new-password"
                            :disable="passwordUpdating || passwordStatusLoading"
                            :rules="passwordConfirmationRules"
                        />
                        <q-btn
                            color="primary"
                            :label="t('settings.changePassword')"
                            type="submit"
                            :loading="passwordUpdating"
                            :disable="passwordStatusLoading"
                        />
                    </q-form>
                </q-card-section>
            </q-card>

            <q-card v-show="activeSettingsSection === 'integrity'" flat bordered>
                <q-card-section class="row items-start justify-between q-gutter-md">
                    <div>
                        <div class="text-h5">{{ t("settings.integrityTitle") }}</div>
                        <p class="text-body2 text-grey-7 q-mb-none">
                            {{ t("settings.integrityDescription") }}
                        </p>
                    </div>
                    <div class="row q-gutter-sm">
                        <q-btn
                            outline
                            color="warning"
                            icon="build"
                            :label="t('settings.attemptRepair')"
                            :loading="integrityRepairing"
                            :disable="integrityChecking || !integrityReport || integrityReport.healthy"
                            @click="confirmStorageRepair"
                        />
                        <q-btn
                            color="primary"
                            icon="fact_check"
                            :label="t('settings.checkFiles')"
                            :loading="integrityChecking"
                            :disable="integrityRepairing"
                            @click="checkStorageIntegrity"
                        />
                    </div>
                </q-card-section>

                <q-separator />

                <q-card-section v-if="integrityChecking" class="row items-center q-gutter-md" aria-live="polite">
                    <q-spinner color="primary" size="32px" />
                    <div>
                        <div class="text-subtitle1 text-weight-medium">{{ t("settings.checking") }}</div>
                        <div class="text-body2 text-grey-7">{{ t("settings.checkingHelp") }}</div>
                    </div>
                </q-card-section>

                <q-card-section v-else-if="integrityError">
                    <q-banner rounded class="bg-red-1 text-negative" aria-live="assertive">
                        <template #avatar><q-icon name="error" /></template>
                        {{ integrityError }}
                    </q-banner>
                </q-card-section>

                <q-card-section v-if="integrityRepairResult">
                    <q-banner rounded class="bg-blue-1 text-primary" aria-live="polite">
                        <template #avatar><q-icon name="build_circle" /></template>
                        <div class="text-weight-medium">
                            {{ t("settings.repairSummary", { backup: integrityRepairResult.backupName }) }}
                        </div>
                        <div class="text-body2">
                            {{ t("settings.repairCounts", { repaired: integrityRepairResult.repair.repaired, recorded: integrityRepairResult.repair.alreadyRecorded, duplicates: integrityRepairResult.repair.duplicates, unmatched: integrityRepairResult.repair.unmatched, failed: integrityRepairResult.repair.failed }) }}
                        </div>
                        <q-list v-if="integrityRepairResult.repair.duplicateDetails?.length" class="q-mt-sm repair-duplicate-list" bordered separator>
                            <q-item v-for="duplicate in integrityRepairResult.repair.duplicateDetails" :key="`${duplicate.path}-${duplicate.existingReference}`">
                                <q-item-section>
                                    <q-item-label class="repair-path">{{ duplicate.path }}</q-item-label>
                                    <q-item-label caption class="repair-path">
                                        {{ t("settings.duplicateReference", { path: duplicate.existingReference }) }}
                                    </q-item-label>
                                </q-item-section>
                            </q-item>
                        </q-list>
                    </q-banner>
                </q-card-section>

                <template v-if="integrityReport">
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
                                {{ integrityReport.healthy ? t("settings.healthy") : t("settings.unhealthy", { missing: missingFileCount, issues: integrityReport.issueCount }) }}
                            </div>
                            <div class="text-body2">
                                {{ t("settings.checked", { references: formatNumber(integrityReport.referencesChecked), files: formatNumber(integrityReport.filesChecked) }) }}
                            </div>
                        </q-banner>
                    </q-card-section>

                    <q-list v-if="integrityReport.issues.length" bordered separator class="q-mx-md q-mb-md rounded-borders">
                        <q-item v-for="(issue, index) in integrityReport.issues" :key="`${issue.kind}-${issue.path}-${index}`">
                            <q-item-section avatar><q-icon name="report_problem" color="warning" /></q-item-section>
                            <q-item-section>
                                <q-item-label class="repair-path">{{ issue.path || formatIssueKind(issue.kind) }}</q-item-label>
                                <q-item-label caption class="repair-path">{{ formatIssueKind(issue.kind) }} · {{ issue.detail }}</q-item-label>
                            </q-item-section>
                        </q-item>
                    </q-list>
                    <div v-if="integrityReport.truncatedIssues" class="text-caption text-grey-7 q-px-md q-pb-md">
                        {{ t("settings.omitted", { count: formatNumber(integrityReport.truncatedIssues) }) }}
                    </div>
                </template>
            </q-card>

            <q-card v-show="activeSettingsSection === 'backups'" flat bordered>
                <q-card-section>
                    <div class="text-h5">{{ t("settings.backupsTitle") }}</div>
                    <p class="text-body2 text-grey-7 q-mb-none">
                        {{ t("settings.backupsDescription") }}
                    </p>
                </q-card-section>

                <q-separator />

                <q-card-section class="q-gutter-md">
                    <div class="backup-actions-grid">
                        <q-input
                            v-model.trim="newBackupName"
                            outlined
                            :label="t('settings.newBackup')"
                            :hint="t('settings.backupHint')"
                            :disable="backupCreating || restoreBusy"
                            :rules="[validBackupName]"
                        />
                        <q-btn
                            color="primary"
                            icon="backup"
                            :label="t('settings.backup')"
                            :loading="backupCreating"
                            :disable="restoreBusy"
                            @click="createServerBackup"
                        />
                    </div>

                    <q-banner v-if="backupLastError" class="bg-red-1 text-negative" rounded>
                        <template #avatar><q-icon name="error" /></template>
                        {{ t("settings.lastBackupFailed", { error: backupLastError }) }}
                    </q-banner>

                    <div class="row items-center justify-between">
                        <div>
                            <div class="text-subtitle1 text-weight-medium">{{ t("settings.availableBackups") }}</div>
                            <div class="text-body2 text-grey-7">
                                {{ t("settings.backupPlacement") }}
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
                        {{ t("settings.noBackups") }}
                    </q-banner>
                    <q-skeleton v-else type="rect" height="88px" />

                    <q-banner rounded class="bg-orange-1 text-orange-10">
                        <template #avatar><q-icon name="warning" /></template>
                        {{ t("settings.restoreWarning") }}
                    </q-banner>

                    <div class="row justify-end">
                        <q-btn
                            color="negative"
                            icon="restore"
                            :label="t('settings.restore')"
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
                            {{ t(restorePhase === "restart-timeout" ? "settings.restartSlow" : "settings.restoreFailedTitle") }}
                        </div>
                        <div class="text-body2">{{ restoreError }}</div>
                    </q-banner>
                </q-card-section>
            </q-card>

            <q-dialog v-model="restoreDialog" persistent>
                <q-card class="restore-dialog">
                    <q-card-section>
                        <div class="text-h5">{{ t("settings.restoreDatabase") }}</div>
                        <div class="text-body2 text-grey-7">
                            {{ t("settings.restoreDescription") }}
                        </div>
                    </q-card-section>
                    <q-separator />
                    <q-card-section>
                        <q-stepper v-model="restoreStep" flat animated color="primary">
                            <q-step :name="1" :title="t('settings.review')" icon="storage" :done="restoreStep > 1">
                                <div class="text-subtitle1">{{ selectedServerBackup?.name }}</div>
                                <p class="text-body2 text-grey-7">
                                    {{ t("settings.reviewHelp") }}
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
                                        <q-item-section side>{{ t(dataDirectoryExists(directory.path) ? "common.detected" : "common.notFound") }}</q-item-section>
                                    </q-item>
                                </q-list>
                                <div class="row items-center q-mt-sm">
                                    <q-btn flat dense color="primary" icon="refresh" :label="t('settings.readData')" :loading="dataDirectoriesLoading" @click="loadDataDirectories" />
                                    <span v-if="dataDirectoriesError" class="text-negative text-caption q-ml-sm">{{ dataDirectoriesError }}</span>
                                </div>
                                <q-banner rounded class="bg-blue-1 text-blue-10 q-mt-md">
                                    {{ t("settings.copyHelp") }}
                                </q-banner>
                            </q-step>

                            <q-step :name="2" :title="t('settings.virtualDirectories')" icon="account_tree" :done="restoreStep > 2">
                                <p class="text-body2 text-grey-7">
                                    {{ t("settings.virtualHelp") }}
                                </p>
                                <div v-if="backupMappingsLoading" class="row items-center q-gutter-sm q-pa-md">
                                    <q-spinner color="primary" />
                                    <span>{{ t("settings.readingMappings") }}</span>
                                </div>
                                <q-banner v-else-if="backupMappingsError" rounded class="bg-red-1 text-negative q-mb-md">
                                    {{ backupMappingsError }}
                                    <template #action>
                                        <q-btn flat color="negative" :label="t('common.tryAgain')" @click="loadBackupMappings(selectedServerBackup?.name ?? '')" />
                                    </template>
                                </q-banner>
                                <template v-else>
                                    <div v-for="(root, index) in restoreSettings.hfsRoots" :key="index" class="hfs-root-grid q-mb-md">
                                        <div class="hfs-root-name"><q-input v-model.trim="root.name" outlined :label="t('settings.virtualName')" /></div>
                                        <div class="hfs-root-path">
                                            <q-select
                                                v-model="root.path"
                                                outlined
                                                use-input
                                                fill-input
                                                hide-selected
                                                new-value-mode="add-unique"
                                                input-debounce="0"
                                                :label="t('settings.serverDirectory')"
                                                :hint="t('settings.serverDirectoryHint')"
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
                                        <div class="hfs-root-action"><q-toggle v-model="root.public" :label="t('common.public')" /></div>
                                        <div class="hfs-root-action"><q-toggle v-model="root.readOnly" :label="t('common.readOnly')" /></div>
                                        <div class="hfs-root-action"><q-btn flat round icon="delete" color="negative" @click="restoreSettings.hfsRoots.splice(index, 1)" /></div>
                                    </div>
                                    <q-btn flat color="primary" icon="add" :label="t('settings.addVirtualRoot')" :disable="restoreSettings.hfsRoots.length >= 64" @click="addRestoreHFSRoot" />
                                </template>
                            </q-step>

                            <q-step :name="3" :title="t('common.confirm')" icon="restore">
                                <q-banner rounded class="bg-orange-1 text-orange-10">
                                    <template #avatar><q-icon name="warning" /></template>
                                    {{ t("settings.confirmRestore") }}
                                </q-banner>
                            </q-step>

                            <template #navigation>
                                <q-stepper-navigation class="row justify-between">
                                    <div>
                                        <q-btn v-if="restoreStep > 1" flat color="primary" :label="t('common.back')" @click="restoreStep--" />
                                    </div>
                                    <div class="q-gutter-sm">
                                        <q-btn flat :label="t('common.cancel')" :disable="restoreBusy" v-close-popup />
                                        <q-btn
                                            v-if="restoreStep < 3"
                                            color="primary"
                                            :label="t('common.continue')"
                                            :disable="backupMappingsLoading || !!backupMappingsError"
                                            @click="restoreStep++"
                                        />
                                        <q-btn v-else color="negative" icon="restore" :label="t('settings.restoreDatabase')" :loading="restoreBusy" @click="restore" />
                                    </div>
                                </q-stepper-navigation>
                            </template>
                        </q-stepper>
                    </q-card-section>
                </q-card>
            </q-dialog>
            </main>
        </div>
    </q-page>
</template>

<script setup lang="ts">
import axios from "axios";
import { useQuasar } from "quasar";
import { useAdminSessionStore } from "src/stores/admin-session";
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";

const $q = useQuasar();
const adminSession = useAdminSessionStore();
const { t, locale } = useI18n();
type SettingsSection = "general" | "bing" | "hfs" | "logging" | "version" | "security" | "integrity" | "backups";
interface SettingsSectionOption { value: SettingsSection; label: string; caption: string; icon: string }
const settingsSections = computed<SettingsSectionOption[]>(() => [
    { value: "general", label: t("settings.sections.general.label"), caption: t("settings.sections.general.caption"), icon: "tune" },
    { value: "bing", label: t("settings.sections.bing.label"), caption: t("settings.sections.bing.caption"), icon: "wallpaper" },
    { value: "hfs", label: t("settings.sections.hfs.label"), caption: t("settings.sections.hfs.caption"), icon: "account_tree" },
    { value: "logging", label: t("settings.sections.logging.label"), caption: t("settings.sections.logging.caption"), icon: "description" },
    { value: "version", label: t("settings.sections.version.label"), caption: t("settings.sections.version.caption"), icon: "info" },
    { value: "security", label: t("settings.sections.security.label"), caption: t("settings.sections.security.caption"), icon: "admin_panel_settings" },
    { value: "integrity", label: t("settings.sections.integrity.label"), caption: t("settings.sections.integrity.caption"), icon: "fact_check" },
    { value: "backups", label: t("settings.sections.backups.label"), caption: t("settings.sections.backups.caption"), icon: "backup" },
]);
const activeSettingsSection = ref<SettingsSection>("general");
const activeSection = computed(() => settingsSections.value.find(section => section.value === activeSettingsSection.value) ?? settingsSections.value[0]!);
interface ServerBackup { name: string; files: number; bytes: number; manifestVersion: number; created: string }
interface BackupStatus { backups: ServerBackup[]; lastError?: string }
interface DataDirectoriesResponse { directories: string[] }
interface BackupMappingsResponse { uploadDirectory: string; downloadDirectory: string; hfsRoots: HFSRoot[] }
interface BuildInfo { version: string; revision: string; modified: boolean; goVersion: string }
interface ReleaseInfo { version: string; url: string; publishedAt: string }
interface VersionStatus { current: BuildInfo; latest?: ReleaseInfo; updateAvailable?: boolean }
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
interface IntegrityRepairReport {
    candidates: number;
    repaired: number;
    alreadyRecorded: number;
    duplicates: number;
    unmatched: number;
    failed: number;
    details: Array<{ path: string; result: string }>;
    duplicateDetails: Array<{ path: string; existingReference: string }>;
}
interface IntegrityRepairResponse {
    backupName: string;
    repair: IntegrityRepairReport;
    integrity: IntegrityReport;
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
const integrityRepairing = ref(false);
const integrityError = ref("");
const integrityReport = ref<IntegrityReport>();
const integrityRepairResult = ref<IntegrityRepairResponse>();
const versionStatus = ref<VersionStatus>();
const versionLoading = ref(true);
const versionChecking = ref(false);
const versionError = ref("");
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
    trustedProxyCIDRs: string[];
    hfsRoots: HFSRoot[];
    localLogEnabled: boolean;
    logMaxFileSize: number;
    logMaxBackups: number;
}
const settings = ref<ApplicationSettings>({ uploadDirectory: "upload", downloadDirectory: "download", publicUploads: true, maxFileCount: 100, maxImageFileSize: 100 * 1024 * 1024, maxHFSFileSize: 1000 * 1024 * 1024, rememberDurationDays: 30, bingSyncEnabled: true, bingSyncIntervalHours: 24, allowedDomainNames: [], trustedProxyCIDRs: [], hfsRoots: [], localLogEnabled: true, logMaxFileSize: 10 * 1024 * 1024, logMaxBackups: 5 });
const restoreSettings = ref<ApplicationSettings>({ uploadDirectory: "upload", downloadDirectory: "download", publicUploads: true, maxFileCount: 100, maxImageFileSize: 100 * 1024 * 1024, maxHFSFileSize: 1000 * 1024 * 1024, rememberDurationDays: 30, bingSyncEnabled: true, bingSyncIntervalHours: 24, allowedDomainNames: [], trustedProxyCIDRs: [], hfsRoots: [], localLogEnabled: true, logMaxFileSize: 10 * 1024 * 1024, logMaxBackups: 5 });
const retainedDirectories = computed(() => [
    { label: t("settings.originalImages"), path: restoreSettings.value.uploadDirectory },
    { label: t("settings.generatedCache"), path: "cache" },
    { label: t("settings.bingArchive"), path: `${restoreSettings.value.downloadDirectory}/bing` },
]);
const allowedDomainsText = ref("");
const trustedProxyCIDRsText = ref("");
const maxImageMiB = ref(100);
const maxHFSMiB = ref(1000);
const logMaxMiB = ref(10);
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
const versionBannerClass = computed(() => versionStatus.value?.updateAvailable === true
    ? "bg-orange-1 text-orange-10"
    : versionStatus.value?.updateAvailable === false
        ? "bg-green-1 text-positive"
        : "bg-blue-1 text-primary");
const versionBannerIcon = computed(() => versionStatus.value?.updateAvailable === true
    ? "system_update_alt"
    : versionStatus.value?.updateAvailable === false ? "check_circle" : "info");
const versionResultMessage = computed(() => versionStatus.value?.updateAvailable === true
    ? t("settings.updateAvailable")
    : versionStatus.value?.updateAvailable === false
        ? t("settings.upToDate")
        : t("settings.developmentVersion"));
const restoreStatus = computed(() => {
    switch (restorePhase.value) {
        case "validating":
            return { title: t("settings.validatingTitle"), detail: t("settings.validatingDetail") };
        case "restarting":
            return { title: t("settings.applyingTitle"), detail: t("settings.applyingDetail") };
        default:
            return { title: t("settings.preparingTitle"), detail: t("settings.preparingDetail") };
    }
});
const requiredPassword = (value: string) => value.length > 0 || t("settings.currentPassword");
const newPasswordRules = [
    (value: string) => new TextEncoder().encode(value).length >= 16 || t("setup.validation.passwordMinimum"),
    (value: string) => new TextEncoder().encode(value).length <= 1024 || t("setup.validation.passwordMaximum"),
    (value: string) => value !== currentPassword.value || t("setup.validation.passwordMismatch"),
];
const passwordConfirmationRules = [
    (value: string) => value === newPassword.value || t("setup.validation.passwordMismatch"),
];

onMounted(() => {
    void loadPasswordStatus();
    void loadSettings();
    void loadBackups();
    void loadVersion();
});

async function loadVersion(check = false) {
    if (check) versionChecking.value = true;
    else versionLoading.value = true;
    versionError.value = "";
    try {
        const response = await axios.get<VersionStatus>(`/api/v1/admin/version${check ? "?check" : ""}`);
        versionStatus.value = response.data;
    } catch (error) {
        const status = axios.isAxiosError(error) ? error.response?.status : undefined;
        if (status === 401 || status === 404) adminSession.clear();
        versionError.value = t("settings.versionCheckFailed");
    } finally {
        versionLoading.value = false;
        versionChecking.value = false;
    }
}

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
        $q.notify({ type: "negative", message: t("settings.loadBackupsFailed") });
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
        integrityError.value = requestError(error, t("settings.checkFailed"));
    } finally {
        integrityChecking.value = false;
    }
}

function confirmStorageRepair() {
    if (!integrityReport.value || integrityReport.value.healthy) return;
    $q.dialog({
        title: t("settings.repairTitle"),
        message: t("settings.repairPrompt"),
        cancel: true,
        persistent: true,
        ok: { label: t("settings.backupRepair"), color: "warning" },
    }).onOk(() => { void attemptStorageRepair(); });
}

async function attemptStorageRepair() {
    const backupName = defaultBackupName().replace("backup-", "repair-before-");
    let dismissBackupNotice: (() => void) | undefined;
    integrityRepairing.value = true;
    integrityError.value = "";
    integrityRepairResult.value = undefined;
    try {
        await axios.post("/api/v1/admin/backups", { name: backupName });
        dismissBackupNotice = $q.notify({ type: "info", message: t("settings.repairBackup"), timeout: 0 });
        await waitForBackup(backupName, 1800);
        dismissBackupNotice();
        dismissBackupNotice = undefined;
        const response = await axios.post<IntegrityRepairResponse>(
            "/api/v1/admin/integrity/repair",
            { backupName },
            { timeout: 0 },
        );
        integrityRepairResult.value = response.data;
        integrityReport.value = response.data.integrity;
        await loadBackups();
        $q.notify({
            type: response.data.repair.failed ? "warning" : "positive",
            message: t("settings.repairDone", { backup: backupName, count: response.data.repair.repaired, duplicates: response.data.repair.duplicates }),
            timeout: 0,
            actions: [{ icon: "close", color: "white" }],
        });
    } catch (error) {
        const status = axios.isAxiosError(error) ? error.response?.status : undefined;
        if (status === 401 || status === 404) adminSession.clear();
        integrityError.value = requestError(error, t("settings.repairFailed"));
        $q.notify({ type: "negative", message: integrityError.value, timeout: 0, actions: [{ icon: "close", color: "white" }] });
    } finally {
        dismissBackupNotice?.();
        integrityRepairing.value = false;
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
        $q.notify({ type: "positive", message: t("settings.backupQueued"), timeout: 0 });
        await waitForBackup(newBackupName.value, 1800);
        window.location.reload();
    } catch (error) {
        const status = axios.isAxiosError(error) ? error.response?.status : undefined;
        if (status === 401 || status === 404) adminSession.clear();
        const message = axios.isAxiosError(error)
            ? (error.response?.data as { error?: string } | undefined)?.error ?? error.message
            : t("settings.backupFailed");
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
    throw new Error(t("settings.backupTimeout"));
}

function validBackupName(value: string) {
    return (/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(value) && !value.endsWith("."))
        || t("settings.backupNameRule");
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
        trustedProxyCIDRsText.value = response.data.trustedProxyCIDRs.join("\n");
        maxImageMiB.value = response.data.maxImageFileSize / 1024 / 1024;
        maxHFSMiB.value = response.data.maxHFSFileSize / 1024 / 1024;
        logMaxMiB.value = response.data.logMaxFileSize / 1024 / 1024;
    } catch (error) {
        if (axios.isAxiosError(error) && (error.response?.status === 401 || error.response?.status === 404)) adminSession.clear();
        $q.notify({ type: "negative", message: t("settings.loadSettingsFailed") });
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
        dataDirectoriesError.value = requestError(error, t("settings.loadDirectoriesFailed"));
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
        backupMappingsError.value = requestError(error, t("settings.loadMappingsFailed"));
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
        settings.value.trustedProxyCIDRs = trustedProxyCIDRsText.value.split(/\r?\n|,/).map(value => value.trim()).filter(Boolean);
        settings.value.maxImageFileSize = Math.round(maxImageMiB.value * 1024 * 1024);
        settings.value.maxHFSFileSize = Math.round(maxHFSMiB.value * 1024 * 1024);
        settings.value.logMaxFileSize = Math.round(logMaxMiB.value * 1024 * 1024);
        await axios.put("/api/v1/admin/settings", settings.value);
        $q.notify({ type: "positive", message: t("settings.settingsSaved") });
        await waitForRestart("Settings update", 30);
        window.location.reload();
    } catch (error) {
        const message = axios.isAxiosError(error)
            ? (error.response?.data as { error?: string } | undefined)?.error ?? error.message
            : t("settings.updateFailed");
        $q.notify({ type: "negative", message });
    } finally {
        settingsSaving.value = false;
    }
}

async function syncBingNow() {
    bingSyncing.value = true;
    try {
        await axios.post("/api/v1/admin/bing/sync");
        $q.notify({ type: "positive", message: t("settings.bingQueued") });
    } catch (error) {
        const status = axios.isAxiosError(error) ? error.response?.status : undefined;
        if (status === 401 || status === 404) adminSession.clear();
        $q.notify({ type: "negative", message: requestError(error, t("settings.bingFailed")) });
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
        $q.notify({ type: "negative", message: t("settings.loadPasswordFailed") });
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
        $q.notify({ type: "positive", message: t("settings.passwordChanged") });
    } catch (error) {
        const message = axios.isAxiosError(error)
            ? (error.response?.data as { error?: string } | undefined)?.error ?? error.message
            : t("settings.passwordFailed");
        $q.notify({ type: "negative", message });
    } finally {
        passwordUpdating.value = false;
    }
}

async function restore() {
    restoreError.value = "";
    restorePhase.value = "validating";
    try {
        if (!selectedServerBackup.value) throw new Error(t("settings.selectBackup"));
        await axios.post("/api/v1/admin/backups/restore", {
            name: selectedServerBackup.value.name,
            hfsRoots: restoreSettings.value.hfsRoots,
        }, { timeout: 0 });
        restorePhase.value = "restarting";
        $q.notify({ type: "positive", message: t("settings.restoreApplying"), timeout: 0 });
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
            : t("settings.restoreFailed");
        const restartTimedOut = restorePhase.value === "restarting";
        restoreError.value = restartTimedOut
            ? t("settings.restoreTimeout")
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
    throw new Error(t("settings.restartFailed", { operation }));
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
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString(locale.value);
}

function formatNumber(value: number) {
    return value.toLocaleString(locale.value);
}
</script>

<style scoped>
.settings-page {
    width: 100%;
}

.settings-shell {
    display: grid;
    grid-template-columns: 260px minmax(0, 1fr);
    gap: 24px;
    width: 100%;
    align-items: start;
}

.settings-sidebar {
    position: sticky;
    top: 66px;
}

.settings-content {
    width: 100%;
    min-width: 0;
}

.settings-mobile-navigation {
    display: none;
}

.repair-duplicate-list {
    background: white;
    color: initial;
}

.repair-path {
    overflow-wrap: anywhere;
    word-break: break-word;
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

@media (max-width: 899px) {
    .settings-shell {
        display: block;
    }

    .settings-sidebar {
        display: none;
    }

    .settings-mobile-navigation {
        display: block;
    }
}
</style>
