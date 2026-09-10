/**
 * The service worker Chrome insists on for an MV3 background: Firefox runs the
 * manifest's list of scripts as an event page, Chrome takes exactly one file.
 */
importScripts('compat.js', 'platforms.js', 'background.js')
