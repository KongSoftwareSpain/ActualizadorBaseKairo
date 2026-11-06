import { createApp } from "vue";
import { createPinia } from "pinia";
import { createRouter, createWebHashHistory } from "vue-router";
import ui from "@nuxt/ui/vue-plugin";
import App from "./App.vue";
import Buttons from "./views/Buttons.vue";
import InputView from "./views/InputView.vue";
import UpdateView from "./views/UpdateView.vue";
import "./style.css";
import { useMigratorStore } from "./stores/migrator.ts";
import { useUserConfigStore } from "./stores/userConfig.ts";

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: "/", redirect: "/input" },
    { path: "/input", component: InputView },
    {
      path: "/update",
      component: UpdateView,
      // The page works on an open connection.
      beforeEnter: () => (useMigratorStore().isConnected ? true : "/input"),
    },
    { path: "/buttons", component: Buttons },
  ],
});

const pinia = createPinia();

const app = createApp(App);
app.use(pinia);
app.use(router);
app.use(ui);

// Init config before app starts but after theme is set
const config = useUserConfigStore(pinia);
await config.init();

app.mount("#app");
