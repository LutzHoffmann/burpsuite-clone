import { useDeferredValue, useState } from 'react';
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

export function SiteMapTree({ nodes, selectedId, filters, onSelect }: SiteMapTreeProps) {
  const deferredText = useDeferredValue(filters.text);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const visibleNodes = filterNodes(nodes, { ...filters, text: deferredText });

  const renderNodes = (items: readonly TargetTreeNode[], depth = 0) => (
    <ul role="group" className="site-map-branch">
      {items.map((node) => {
        const key = `${node.id}-${nodeLabel(node)}`;
        const expandable = node.children.length > 0;
        const expanded = Boolean(collapsed[key]);
        const selected = node.id === selectedId;
        return <li key={key}>
          <button
            aria-expanded={expandable ? expanded : undefined}
            aria-level={depth + 1}
            aria-selected={selected || undefined}
            className={`site-map-node ${selected ? 'selected' : ''} ${node.inScope ? '' : 'out-of-scope'}`}
            onClick={() => {
              if (expandable) setCollapsed({ ...collapsed, [key]: !expanded });
              if (node.method) onSelect(node.id);
            }}
            role="treeitem"
            type="button"
          >
            <span className="tree-indent" style={{ width: `${depth * 13}px` }} />
            <span className="tree-disclosure">{expandable ? (expanded ? 'v' : '>') : '-'}</span>
            {node.method && <span className={`method method-${node.method.toLowerCase()}`}>{node.method}</span>}
            <span>{node.method ? node.path || '/' : nodeLabel(node)}</span>
            <small>{node.count}</small>
          </button>
          {expandable && expanded && renderNodes(node.children, depth + 1)}
        </li>;
      })}
    </ul>
  );

  return <div className="site-map-tree" role="tree" aria-label="Target site map">
    {visibleNodes.length > 0 ? renderNodes(visibleNodes) : <p className="empty-state">No endpoints match these filters.</p>}
  </div>;
}
