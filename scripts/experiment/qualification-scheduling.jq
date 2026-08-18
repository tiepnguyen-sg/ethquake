.[0] as $nodes | .[1] as $pods |
($nodes.items | length) == 4 and
([$nodes.items[].metadata.labels["dev.ethquake.participant"]] | sort) ==
  ["ethquake-p1", "ethquake-p2", "ethquake-p3", "ethquake-p4"] and
all($nodes.items[];
  . as $node |
  ([$pods.items[] |
    select(.spec.nodeName == $node.metadata.name and .status.phase != "Succeeded")] | length) >= 3)
