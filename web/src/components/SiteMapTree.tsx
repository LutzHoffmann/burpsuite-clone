import { useDeferredValue, useEffect, useState } from 'react';
import type { TargetTreeNode } from '../types';

export type TreeFilters = {
  text: string;
  scope: 'all' | 'in' | 'out';
  method: string;
  status: string;
  mime: string;
};

type SiteMapTreeProps = {
  nodes: readonly TargetTreeNode[];
  selectedId: number | null;
  filters: TreeFilters;
  onSelect: (id: number) => void;
};

type VisibleNode = { node: TargetTreeNode; key: string; parentKey: string | null };

function hasStatusFamily(statuses: number[], family: string) {
  return family === 'all' || statuses.some((status) => `${Math.floor(status / 100)}xx` === family);
}

function hasMime(node: TargetTreeNode, mime: string) {
  return mime === 'all' || [...node.requestMimes, ...node.responseMimes].includes(mime);
}

function nodeLabel(node: TargetTreeNode) {
  if (node.method) return `${node.method} ${node.path || '/'}`;
  if (node.host) return node.port ? `${node.host}:${node.port}` : node.host;
  return node.scheme || 'Site map node';
}

function nodeKey(node: TargetTreeNode) {
  return `${node.id}-${nodeLabel(node)}`;
}

function filterNodes(nodes: readonly TargetTreeNode[], filters: TreeFilters): TargetTreeNode[] {
  const normalizedText = filters.text.trim().toLowerCase();
  return nodes.flatMap((node) => {
    const children = filterNodes(node.children, filters);
    const isEndpoint = Boolean(node.method);
    const textMatches = !normalizedText || [node.scheme, node.host, node.path, node.method].join(' ').toLowerCase().includes(normalizedText);
    const scopeMatches = filters.scope === 'all' || (filters.scope === 'in' ? node.inScope : !node.inScope);
    const methodMatches = filters.method === 'all' || node.method === filters.method;
    const matches = textMatches && scopeMatches && methodMatches && hasStatusFamily(node.statuses, filters.status) && hasMime(node, filters.mime);
    if (!isEndpoint && children.length === 0 && !matches) return [];
    if (isEndpoint && !matches) return [];
    return [{ ...node, children }];
  });
}

function flattenExpanded(nodes: readonly TargetTreeNode[], expanded: Record<string, boolean>, parentKey: string | null = null): VisibleNode[] {
  return nodes.flatMap((node) => {
    const key = nodeKey(node);
    const visible = [{ node, key, parentKey }];
    return expanded[key] ? [...visible, ...flattenExpanded(node.children, expanded, key)] : visible;
  });
}

export function SiteMapTree({ nodes, selectedId, filters, onSelect }: SiteMapTreeProps) {
  const deferredText = useDeferredValue(filters.text);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [activeKey, setActiveKey] = useState<string | null>(null);
  const visibleNodes = filterNodes(nodes, { ...filters, text: deferredText });
  const focusableNodes = flattenExpanded(visibleNodes, expanded);
  const currentKey = focusableNodes.some((node) => node.key === activeKey) ? activeKey : focusableNodes[0]?.key ?? null;

  useEffect(() => {
    if (activeKey) document.getElementById(`site-map-item-${activeKey}`)?.focus();
  }, [activeKey, expanded]);

  const moveFocus = (key: string | null) => {
    if (key) setActiveKey(key);
  };

  const handleKeyDown = (event: React.KeyboardEvent<HTMLDivElement>, node: TargetTreeNode, key: string, parentKey: string | null) => {
    const index = focusableNodes.findIndex((item) => item.key === key);
    const expandable = node.children.length > 0;
    if (event.key === 'ArrowDown' && index < focusableNodes.length - 1) {
      event.preventDefault();
      moveFocus(focusableNodes[index + 1].key);
    }
    if (event.key === 'ArrowUp' && index > 0) {
      event.preventDefault();
      moveFocus(focusableNodes[index - 1].key);
    }
    if (event.key === 'ArrowRight') {
      event.preventDefault();
      if (expandable && !expanded[key]) setExpanded({ ...expanded, [key]: true });
      else if (expandable) moveFocus(nodeKey(node.children[0]));
    }
    if (event.key === 'ArrowLeft') {
      event.preventDefault();
      if (expandable && expanded[key]) setExpanded({ ...expanded, [key]: false });
      else moveFocus(parentKey);
    }
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      if (expandable) setExpanded({ ...expanded, [key]: !expanded[key] });
      if (node.method) onSelect(node.id);
    }
  };

  const renderNodes = (items: readonly TargetTreeNode[], depth = 0, parentKey: string | null = null) => items.map((node) => {
    const key = nodeKey(node);
    const expandable = node.children.length > 0;
    const groupID = `site-map-group-${key}`;
    const selected = node.id === selectedId;
    return <li key={key}>
      <div
        aria-controls={expandable ? groupID : undefined}
        aria-expanded={expandable ? Boolean(expanded[key]) : undefined}
        aria-level={depth + 1}
        aria-owns={expandable ? groupID : undefined}
        aria-selected={selected || undefined}
        className={`site-map-node ${selected ? 'selected' : ''} ${node.inScope ? '' : 'out-of-scope'}`}
        id={`site-map-item-${key}`}
        onClick={() => {
          setActiveKey(key);
          if (expandable) setExpanded({ ...expanded, [key]: !expanded[key] });
          if (node.method) onSelect(node.id);
        }}
        onKeyDown={(event) => handleKeyDown(event, node, key, parentKey)}
        role="treeitem"
        tabIndex={key === currentKey ? 0 : -1}
      >
        <span aria-hidden="true" className="tree-indent" style={{ width: `${depth * 13}px` }} />
        <span aria-hidden="true" className="tree-disclosure">{expandable ? (expanded[key] ? 'v' : '>') : '-'}</span>
        {node.method && <span className={`method method-${node.method.toLowerCase()}`}>{node.method}</span>}
        <span>{node.method ? node.path || '/' : nodeLabel(node)}</span>
        <small aria-hidden="true">{node.count}</small>
      </div>
      {expandable && <ul aria-labelledby={`site-map-item-${key}`} className="site-map-branch" hidden={!expanded[key]} id={groupID} role="group">{renderNodes(node.children, depth + 1, key)}</ul>}
    </li>;
  });

  return <ul className="site-map-tree" role="tree" aria-label="Target site map">
    {visibleNodes.length > 0 ? renderNodes(visibleNodes) : <li className="empty-state">No endpoints match these filters.</li>}
  </ul>;
}
