import { defineRouter } from "@quasar/app-vite";
import {
    createMemoryHistory,
    createRouter,
    createWebHashHistory,
    createWebHistory,
} from "vue-router";
import routes from "./routes";
import { RouteName } from "src/common";
import { checkAdminAuthentication, isAdminAuthenticated } from "src/stores/admin-session";
import { checkServerInitialization } from "src/stores/server-setup";

/*
 * If not building with SSR mode, you can
 * directly export the Router instantiation;
 *
 * The function below can be async too; either use
 * async/await or return a Promise which resolves
 * with the Router instance.
 */

export default defineRouter(function(/* { store, ssrContext } */) {
    const isServer = import.meta.env.SSR;
    const routerMode = import.meta.env.VUE_ROUTER_MODE;
    const routerBase = import.meta.env.BASE_URL;

    const createHistory = isServer
        ? createMemoryHistory
        : (routerMode === "history" ? createWebHistory : createWebHashHistory);

    const Router = createRouter({
        scrollBehavior: () => ({ left: 0, top: 0 }),
        routes,

        // Leave this as is and make changes in quasar.conf.js instead!
        // quasar.conf.js -> build -> vueRouterMode
        // quasar.conf.js -> build -> publicPath
        history: createHistory(
            isServer ? void 0 : routerBase,
        ),
    });

    Router.beforeEach(async to => {
        const initialized = await checkServerInitialization();
        if (initialized === false && to.name !== "setup") {
            return { name: "setup" };
        }
        if (initialized === true && to.name === "setup") {
            return { name: RouteName.Upload };
        }
        if (to.matched.some(record => record.meta.requiresAdmin) && !isAdminAuthenticated()) {
            if (!await checkAdminAuthentication()) {
                return { name: "upload" };
            }
        }
        return true;
    });

    return Router;
});


