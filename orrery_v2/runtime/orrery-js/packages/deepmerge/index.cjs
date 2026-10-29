// Copyright 2026 Minvia. SPDX-License-Identifier: Apache-2.0
// Orrery independently implemented deep merge for build-time Svelte options.

const UNSAFE_KEYS = new Set(['__proto__', 'constructor', 'prototype'])

function ownKeys(value) {
  return Object.keys(value).concat(
    typeof Object.getOwnPropertySymbols === 'function'
      ? Object.getOwnPropertySymbols(value).filter((key) => Object.prototype.propertyIsEnumerable.call(value, key))
      : []
  )
}

function isMergeableObject(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return false
  const prototype = Object.getPrototypeOf(value)
  return prototype === Object.prototype || prototype === null
}

function emptyTarget(value) {
  return Array.isArray(value) ? [] : {}
}

function clone(value, options) {
  return options.clone !== false && options.isMergeableObject(value)
    ? merge({}, value, options)
    : value
}

function defaultArrayMerge(target, source, options) {
  return target.concat(source).map((value) => clone(value, options))
}

function getMergeFunction(key, options) {
  const custom = options.customMerge && options.customMerge(key)
  return typeof custom === 'function' ? custom : merge
}

function mergeObject(target, source, options) {
  const destination = {}
  for (const key of ownKeys(target)) {
    if (typeof key === 'string' && UNSAFE_KEYS.has(key)) continue
    Object.defineProperty(destination, key, { value: clone(target[key], options), enumerable: true, writable: true, configurable: true })
  }
  for (const key of ownKeys(source)) {
    if (typeof key === 'string' && UNSAFE_KEYS.has(key)) continue
    const hasTarget = Object.prototype.hasOwnProperty.call(target, key)
    const value = hasTarget && options.isMergeableObject(source[key])
      ? getMergeFunction(key, options)(target[key], source[key], options)
      : clone(source[key], options)
    Object.defineProperty(destination, key, { value, enumerable: true, writable: true, configurable: true })
  }
  return destination
}

function merge(target, source, options = {}) {
  options.arrayMerge = options.arrayMerge || defaultArrayMerge
  options.isMergeableObject = options.isMergeableObject || isMergeableObject
  options.cloneUnlessOtherwiseSpecified = clone
  const sourceIsArray = Array.isArray(source)
  const targetIsArray = Array.isArray(target)
  if (sourceIsArray !== targetIsArray) return clone(source, options)
  if (sourceIsArray) return options.arrayMerge(target, source, options)
  return mergeObject(target, source, options)
}

merge.all = function all(values, options) {
  if (!Array.isArray(values)) throw new Error('first argument should be an array')
  return values.reduce((target, source) => merge(target, source, options), {})
}

module.exports = merge
