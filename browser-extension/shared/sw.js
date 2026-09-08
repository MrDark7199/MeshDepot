/**
 * The service worker Chrome insists on for a Manifest V3 background.
 *
 * Firefox takes a list of scripts in the manifest and runs them as an event
 * page; Chrome takes exactly one file and runs it as a worker. So the same
 * scripts are pulled in here, in the same order, and nothing else differs -
 * background.js touches no document and is at home in either.
 */
importScripts('compat.js', 'platforms.js', 'background.js')
