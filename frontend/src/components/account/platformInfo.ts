import { api } from '../../services/api'
import { For } from 'solid-js'

export interface PlatformInfo {
  supportsAutoLogin: boolean
  requiresTotpSeed?: boolean
  requiresUsername?: boolean
  hasToken?: boolean
  hint: string
  tokenLabel: string
  tokenPlaceholder: string
}

export const PLATFORM_INFO: Record<string, PlatformInfo> = {
  printables: {
    supportsAutoLogin: true,
    hasToken: false,
    hint: 'Free models work without login. For paid/private models, save your email + password for auto-login.',
    tokenLabel: '',
    tokenPlaceholder: '',
  },
  thingiverse: {
    supportsAutoLogin: false,
    requiresUsername: true,
    hint: 'Get your App Token at thingiverse.com/developers → Create App → copy the App Token.',
    tokenLabel: 'App Token',
    tokenPlaceholder: 'Paste your App Token here…',
  },
  makerworld: {
    supportsAutoLogin: true,
    requiresTotpSeed: true,
    hasToken: false,
    hint: 'Use your Bambu Lab account credentials. 2FA must be enabled in your Bambu Lab account (authenticator app - not email). Enter the seed (secret key) from your authenticator setup.',
    tokenLabel: '',
    tokenPlaceholder: '',
  },
  thangs: {
    supportsAutoLogin: true,
    hasToken: false,
    hint: 'Enter your Thangs email and password for automatic login.',
    tokenLabel: '',
    tokenPlaceholder: '',
  },
  cults3d: {
    supportsAutoLogin: true,
    hasToken: true,
    hint: 'API key (cults3d.com/en/api/keys) syncs your collections reliably. Format: username:apikey. Email + password are only needed to download files.',
    tokenLabel: 'API Key',
    tokenPlaceholder: 'username:apikey (e.g. mrdark7199:abc123…)',
  },
  myminifactory: {
    supportsAutoLogin: false,
    requiresUsername: true,
    hint: 'API key + your MyMiniFactory username. No password/login needed.',
    tokenLabel: 'API Key',
    tokenPlaceholder: 'Paste your API key here…',
  },
}
