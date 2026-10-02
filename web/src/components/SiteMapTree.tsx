import { useDeferredValue, useEffect, useRef, useState, type KeyboardEvent } from 'react';
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

type NodeContext = { scheme: string; host: string; port: number; path: string[] };
type VisibleNode = { node: TargetTreeNode; key: string; parentKey: string | null };

const rootContext: NodeContext = { scheme: '', host: '', port: 0, path: [] };

function hasStatusFamily(statuses: number[], family: string) {
  return family === 'all' || statuses.some((status) => `${Math.floor(status / 100)}xx` === family);
}

function hasMime(node: TargetTreeNode, mime: string) {
  return mime === 'all' || [...node.requestMimes, ...node.responseMimes].includes(mime);
}

function contextFor(node: TargetTreeNode, parent: NodeContext): NodeContext {
  const path = node.path ? (node.path.startsWith('/') ? node.path.split('/').slice(1) : [...parent.path, node.path]) : parent.path;
  return { scheme: node.scheme || parent.scheme, host: node.host || parent.host, port: node.port || parent.port, path };
}

function pathLabel(context: NodeContext) {
  return context.path.length > 0 ? `/${context.path.join('/')}` : '/';
}

function nodeLabel(node: TargetTreeNode, context: NodeContext) {
  if (node.method) return `${node.method} ${pathLabel(context)}`;
  if (node.host) return node.port ? `${node.host}:${node.port}` : node.host;
  if (node.path) return node.path;
  if (!node.scheme && !node.host && !node.method) return '(empty segment)';
  return node.scheme || 'Site map node';
}

function nodeKey(parentKey: string | null, node: TargetTreeNode) {
  const segment = node.method ? `endpoint:${node.method}:${node.id}` : node.host || node.scheme || node.port ? `authority:${node.scheme}:${node.host}:${node.port}` : `path:${node.path}`;
  return `${parentKey ? `${parentKey}/` : ''}${encodeURIComponent(segment)}`;
}

function itemID(key: string) {
  return `site-map-item-${encodeURIComponent(key)}`;
}

function groupID(key: string) {
  return `site-map-group-${encodeURIComponent(key)}`;
}

function filterNodes(nodes: readonly TargetTreeNode[], filters: TreeFilters, parentContext = rootContext): TargetTreeNode[] {
  const normalizedText = filters.text.trim().toLowerCase();
  return nodes.flatMap((node) => {
    const context = contextFor(node, parentContext);
    const children = filterNodes(node.children, filters, context);
    const isEndpoint = Boolean(node.method);
    const textMatches = !normalizedText || [context.scheme, context.host, pathLabel(context), node.method].join(' ').toLowerCase().includes(normalizedText);
    const scopeMatches = filters.scope === 'all' || (filters.scope === 'in' ? node.inScope : !node.inScope);
    const methodMatches = filters.method === 'all' || node.method === filters.method;
    const matches = textMatches && scopeMatches && methodMatches && hasStatusFamily(node.statuses, filters.status) && hasMime(node, filters.mime);
    if (!isEndpoint && children.length === 0) return [];
    if (isEndpoint && !matches) return [];
    return [{ ...node, children }];
  });
}

function flattenExpanded(nodes: readonly TargetTreeNode[], expanded: Record<string, boolean>, parentKey: string | null = null): VisibleNode[] {
  return nodes.flatMap((node) => {
    const key = nodeKey(parentKey, node);
    const visible = [{ node, key, parentKey }];
    return expanded[key] ? [...visible, ...flattenExpanded(node.children, expanded, key)] : visible;
  });
}

export function SiteMapTree({ nodes, selectedId, filters, onSelect }: SiteMapTreeProps) {
  const deferredText = useDeferredValue(filters.text);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [activeKey, setActiveKey] = useState<string | null>(null);
  const hadTreeFocus = useRef(false);
  const restoreFocus = useRef(false);
  const visibleNodes = filterNodes(nodes, { ...filters, text: deferredText });
  const focusableNodes = flattenExpanded(visibleNodes, expanded);
  const currentKey = focusableNodes.some((node) => node.key === activeKey) ? activeKey : focusableNodes[0]?.key ?? null;

  useEffect(() => {
    if (activeKey && currentKey !== activeKey) {
      restoreFocus.current = hadTreeFocus.current;
      setActiveKey(currentKey);
    }
  }, [activeKey, currentKey]);

  useEffect(() => {
    if (currentKey && restoreFocus.current) {
      document.getElementById(itemID(currentKey))?.focus();
      restoreFocus.current = false;
    }
  }, [currentKey]);

  const moveFocus = (key: string | null) => {
    if (key) {
      restoreFocus.current = true;
      setActiveKey(key);
    }
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>, node: TargetTreeNode, key: string, parentKey: string | null) => {
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
      else if (expandable) moveFocus(nodeKey(key, node.children[0]));
    }
    if (event.key === 'ArrowLeft') {
      event.preventDefault();
      if (expandable && expanded[key]) setExpanded({ ...expanded, [key]: false });
      else moveFocus(parentKey);
    }
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      if (expandable) setExpanded({ ...expanded, [key]: !expanded[key] });
      if (node.method && node.id !== 0) onSelect(node.id);
    }
  };

  const renderNodes = (items: readonly TargetTreeNode[], depth = 0, parentKey: string | null = null, parentContext = rootContext) => items.map((node) => {
    const key = nodeKey(parentKey, node);
    const context = contextFor(node, parentContext);
    const expandable = node.children.length > 0;
    const group = groupID(key);
    const endpoint = node.method !== '' && node.id !== 0;
    const selected = endpoint && node.id === selectedId;
    return <li key={key}>
      <div
        aria-controls={expandable ? group : undefined}
        aria-expanded={expandable ? Boolean(expanded[key]) : undefined}
        aria-level={depth + 1}
        aria-owns={expandable ? group : undefined}
        aria-selected={selected || undefined}
        className={`site-map-node ${selected ? 'selected' : ''} ${node.inScope ? '' : 'out-of-scope'}`}
        id={itemID(key)}
        onClick={() => {
          setActiveKey(key);
          if (expandable) setExpanded({ ...expanded, [key]: !expanded[key] });
          if (endpoint) onSelect(node.id);
        }}
        onKeyDown={(event) => handleKeyDown(event, node, key, parentKey)}
        role="treeitem"
        tabIndex={key === currentKey ? 0 : -1}
      >
        <span aria-hidden="true" className="tree-indent" style={{ width: `${depth * 13}px` }} />
        <span aria-hidden="true" className="tree-disclosure">{expandable ? (expanded[key] ? 'v' : '>') : '-'}</span>
        {node.method && <span className={`method method-${node.method.toLowerCase()}`}>{node.method}</span>}
        <span>{node.method ? pathLabel(context) : nodeLabel(node, context)}</span>
        <small aria-hidden="true">{node.count}</small>
      </div>
      {expandable && <ul aria-labelledby={itemID(key)} className="site-map-branch" hidden={!expanded[key]} id={group} role="group">{renderNodes(node.children, depth + 1, key, context)}</ul>}
    </li>;
  });

  return <ul
    aria-label="Target site map"
    className="site-map-tree"
    onBlur={(event) => { hadTreeFocus.current = event.currentTarget.contains(event.relatedTarget); }}
    onFocus={(event) => {
      hadTreeFocus.current = true;
      const focusedKey = focusableNodes.find((node) => itemID(node.key) === (event.target as HTMLElement).id)?.key;
      if (focusedKey && focusedKey !== activeKey) setActiveKey(focusedKey);
    }}
    role="tree"
  >
    {visibleNodes.length > 0 ? renderNodes(visibleNodes) : <li className="empty-state">No endpoints match these filters.</li>}
  </ul>;
}
