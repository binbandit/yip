import '@fontsource-variable/inter/wght.css';
import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { app } from './lib/state/app.svelte';

const target = document.getElementById('app');
if (!target) throw new Error('Missing #app');
mount(App, { target });
app.start();
