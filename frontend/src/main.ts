import { createApp } from "vue";
import { createPinia } from "pinia";
import { createRouter, createWebHashHistory } from "vue-router";
import ui from "@nuxt/ui/vue-plugin";
import App from "./App.vue";
import Buttons from "./views/Buttons.vue";
import HomeView from "./views/HomeView.vue";
import "./style.css";
import { useUserConfigStore } from "./stores/userConfig.ts";

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: "/", component: HomeView },
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
