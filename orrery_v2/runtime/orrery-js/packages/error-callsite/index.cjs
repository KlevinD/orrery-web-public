// Copyright 2026 Minvia. SPDX-License-Identifier: Apache-2.0
'use strict';

const captured = new WeakMap();

/** Capture V8 CallSites while preserving the error's normal lazy stack value. */
module.exports = function errorCallsite(error) {
  if (!error || (typeof error !== 'object' && typeof error !== 'function')) return undefined;
  if (captured.has(error)) return captured.get(error);
  const descriptor = Object.getOwnPropertyDescriptor(Error, 'prepareStackTrace');
  const previous = Error.prepareStackTrace;
  let frames;
  Error.prepareStackTrace = (value, sites) => {
    if (value === error) frames = sites;
    if (typeof previous === 'function') return previous(value, sites);
    return [Error.prototype.toString.call(value), ...sites.map(site => `    at ${site}`)].join('\n');
  };
  try {
    // Accessing an already formatted stack cannot recover its native CallSites.
    void error.stack;
  } finally {
    if (descriptor) Object.defineProperty(Error, 'prepareStackTrace', descriptor);
    else delete Error.prepareStackTrace;
  }
  if (frames) captured.set(error, frames);
  return frames;
};
