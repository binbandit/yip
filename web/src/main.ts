import '@fontsource-variable/inter/wght.css';
import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { app } from './lib/state/app.svelte';

const target = document.getElementById('app');
if (!target) throw new Error('Missing #app');
mount(App, { target });
app.start();

// Dev only: hold ⌘C/Ctrl+C, then click an element to copy its .svelte
// file:line:col and component stack for a coding agent. Builds drop this.
if (import.meta.env.DEV) void import('point-to-svelte');
