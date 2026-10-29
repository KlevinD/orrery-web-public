// Copyright 2026 Minvia. SPDX-License-Identifier: Apache-2.0

/** Index LF boundaries once, then locate arbitrary UTF-16 offsets by binary search. */
export function getLocator(source, options = {}) {
  const starts = [0];
  for (let position = source.indexOf('\n'); position !== -1; position = source.indexOf('\n', position + 1)) {
    starts.push(position + 1);
  }
  const lineOffset = options.offsetLine || 0;
  const columnOffset = options.offsetColumn || 0;

  return (search, index) => {
    const character = typeof search === 'string' ? source.indexOf(search, index) : search;
    if (!Number.isFinite(character) || character < 0 || character > source.length) return undefined;
    let low = 0;
    let high = starts.length;
    while (low + 1 < high) {
      const middle = (low + high) >>> 1;
      if (starts[middle] <= character) low = middle;
      else high = middle;
    }
    return { line: low + lineOffset, column: character - starts[low] + columnOffset, character };
  };
}

export function locate(source, search, options = {}) {
  return getLocator(source, options)(search, options.startIndex);
}
