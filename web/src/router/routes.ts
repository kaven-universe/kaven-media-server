/********************************************************************
 * @author:      Kaven
 * @email:       kaven@wuwenkai.com
 * @website:     http://blog.kaven.xyz
 * @file:        [kaven-image] /src/router/routes.ts
 * @create:      2022-08-24 16:06:18.350
 * @modify:      2022-09-01 18:26:56.063
 * @version:     0.0.2
 * @times:       8
 * @lines:       46
 * @copyright:   Copyright © 2022 Kaven. All Rights Reserved.
 * @description: [description]
 * @license:     [license]
 ********************************************************************/

import { RouteName } from "src/common";
import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
    {
        path: "/",
        component: () => import("layouts/MainLayout.vue"),
        children: [
            {
                name: RouteName.Upload,
                path: "",
                component: () => import("pages/IndexPage.vue"),
            },
            {
                name: RouteName.HFS,
                path: "/hfs/:hfsPath(.*)*",
                component: () => import("pages/FileExplorerPage.vue"),
            },
        ],
    },

    // Always leave this as last one,
    // but you can also remove it
    {
        path: "/:catchAll(.*)*",
        component: () => import("pages/ErrorNotFound.vue"),
    },
];

export default routes;
