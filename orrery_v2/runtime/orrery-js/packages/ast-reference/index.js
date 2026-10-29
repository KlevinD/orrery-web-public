// Copyright 2026 Minvia. SPDX-License-Identifier: Apache-2.0

/** Classify the identifier positions used by ESTree scope walkers. */
export default function isReference(node, parent) {
  if (node.type === 'MemberExpression') {
    return !node.computed && isReference(node.object, node);
  }
  if (node.type !== 'Identifier') return false;
  if (!parent) return true;

  // A computed property is an expression; a literal property name is not.
  switch (parent.type) {
    case 'MemberExpression':
      return parent.object === node || !!parent.computed;
    case 'Property':
      return parent.value === node || !!parent.computed;
    case 'MetaProperty':
    case 'LabeledStatement':
    case 'BreakStatement':
    case 'ContinueStatement':
      return false;
    case 'MethodDefinition':
      return !!parent.computed;
    case 'PropertyDefinition':
      return parent.value === node || !!parent.computed;
    case 'ExportSpecifier':
      return parent.local === node;
    case 'ImportSpecifier':
      return parent.local === node;
    default:
      // Scope walkers also need declaration/binding identifiers.
      return true;
  }
}
