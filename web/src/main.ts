import '@fontsource-variable/hanken-grotesk/wght.css';
import '@fontsource/bricolage-grotesque/latin-600.css';
import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { app } from './lib/state/app.svelte';

const target = document.getElementById('app');
if (!target) throw new Error('Missing #app');
mount(App, { target });
app.start();
