import type { Component } from 'svelte';
import { Eye, Info, KeyRound, SquareTerminal, TriangleAlert } from '@lucide/svelte';
import type { FactIcon } from './machines';

/** The Lucide glyph for each icon a machine fact or limit names. */
export const FACT_ICONS: Record<FactIcon, Component> = { alert: TriangleAlert, info: Info, terminal: SquareTerminal, key: KeyRound, eye: Eye };
