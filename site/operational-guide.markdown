# Concepts

This section describes some essential concepts with which you will
need to be familiar before moving on to the example deployment
scenarios.

## Peer

A peer is a host on which you have installed weave.

## Peer Name

Peers in the weave network are identified by a 48-bit value formatted
like an ethernet MAC address e.g. `01:23:45:67:89:ab`. This 'peer
name' is used for various purposes:

* Routing of packets between containers on the overlay network
* Recording the origin peer of DNS entries
* Recording ownership of IP address ranges

Whilst the first two uses tolerate the peer name changing on router
restarts, the third use means that address space can be 'lost' (e.g.
recorded as being owned by a now non-existent peer name) and so it is
desirable for the peer name to be as stable as possible across
restarts. Consequently when the router is launched on a host it
derives its peer name in order of preference:

* From the command line; specifier is responsible for uniqueness and
  stability
* From the BIOS product UUID, which is generally stable across restarts
  and unique across different physical hardware and cloned VMs
* From a random value, unique across different physical hardware and
  cloned VMs but not stable across restarts

More important still than the stability constraint is uniqueness;
address space lost due to a peer name change can be recovered with an
administrative action, but if two or more peers share the same name
chaos will ensue, including but not limited to double allocation of
addresses and inability to route packets on the overlay network.

The best strategy for assigning peer names depends on the type and
method of your particular deployment and is discussed in more detail
below.

## Peer Discovery

Peer discovery is a mechanism which allows peers to learn about new
weave hosts from existing peers without being explicitly told. Peer
discovery is enabled by default.

## Network Partition

A network partition is a transient condition whereby some arbitrary
subsets of peers are unable to communicate with each other. Weave is
designed to allow peers and their containers to make maximum safe
progress under conditions of partition, healing automatically once the
partition is over.

## IP Address Manager (IPAM)

IPAM is the subsystem responsible for dividing up a large contiguous
block of IP addresses (known as the IP allocation range) amongst peers
so that individual addresses may be uniquely assigned to containers
anywhere on the overlay network.

When a new network is formed an initial division of the IP allocation
range must be made. Two (mutually exclusive) mechanisms with different
tradeoffs are provided to perform this task: seeding and consensus.

## Seeding

Seeding requires each peer to be told the list of peer names amongst
which the address space is to be divided initially. There are some
constraints and consequences:

* Every peer added to the network _must_ receive the same seed list,
  for all time, or they will not be able to join together to form a
  single cohesive whole
* Because the 'product UUID' and 'random value' methods of peer name
  assignment are unpredictable, the end user must by necessity also
  specify peer names
* Even though every peer _must_ receive the same seed, that seed does
  _not_ have to include every peer in the network, nor does it have to
  be updated when new peers are added (in fact due to the first
  constraint above it may not be)

Example configurations are given in the section on deployment
scenarios.

## Consensus

Alternatively, when a new network is formed for the first time peers
can be configured to co-ordinate amongst themselves to automatically
divide up the IP allocation range - this process is known as
consensus, and is implemented via a leader election algorithm.
Consensus based bootstrapping requires each peer to be told the total
number of expected peers (the 'initial peer count') in order to avoid
small independent groups of peers from electing different leaders
under conditions of partition.

Example configurations are given in the section on deployment
scenarios.

## Electors and Observers

When using consensus-based configuration then by default every peer
included in the network is a candidate for leadership. This does not
always make sense (for example when adding relatively short lived
peers to an existing cluster in response to a scale-out event) and so
an option is provided to start a peer as an _observer_. Such peers
will not put themselves forward as leaders, but will abide by the
results of an election amongst elector-peers.

Example configurations are given in the section on deployment
scenarios.

## Persistence

If you relaunch weave with _exactly_ the same parameters as the
previous invocation (as will happen if it is restarted automatically
by Docker), certain information is remembered:

* Runtime modifications to the target peer list enacted via the
  command line
* The division of the IP allocation range amongst peers
* Allocation of addresses to containers

The persistence of this information is managed transparently inside
the router container but can be destroyed explicitly if necessary.

# Deployment Scenarios

This section discusses various deployment scenarios, documenting the
best-practice operational guidance for each use case.

## Manual/Incremental

This scenario is intended for evaluation, not production use. The
resulting weave network should survive host reboots.

### Adding a Peer

On initial peer:

    weave launch
    weave consense

Or on each subsequent peer:

    weave launch
    weave connect <existing peers> ...
    weave consense

The consense step is required to ensure that the new peer has joined
to the existing network; you must wait for this to complete before
moving on to further new peers.

Then, optionally on each existing peer:

    weave connect <new peer>

This step is not mandatory, but improves the robustness of network
reformation in the face of node failure as _nodes do not remember to
connect to discovered peers on restart_. (For example, if you did the
following:

    host-A$ weave launch; weave consense
    host-B$ weave launch; weave connect host-A; weave consense
    host-C$ weave launch; weave connect host-B; weave consense

host-C's ability to connect to host-A would be entirely dependent on
gossip discovery via host-B; if the whole network rebooted and host-B
did not come back up then host-A and host-C would not connect to each
other.)

### Removing a Peer

On peer to be removed:

    weave reset

On each remaining peer:

    weave forget <removed peer>

## Uniform Fixed Cluster

A uniform fixed cluster has the following characteristics:

* Each node has identical configuration
* There is a controlled process for adding or removing nodes. The end
  user is responsible for ensuring that only one instance of the
  process is in-flight at a time

### Bootstrapping

This scenario describes a production deployment of a fixed number of
N nodes (N=1 in the degenerate case).

On each peer:

    weave launch --initial-peer-count=N
    weave connect <inital peer list>

The initial peer list may contain the address of the peer to which it
is being supplied, so the configuration may be identical for each
peer.

Then on _any_ peer:

    weave status

To check that all connections have established successfully followed
by:

    weave consense

To force consensus - future IP address allocations can now continue
under partition and it is safe to add new peers.

### Adding a Peer

On new peer:

    weave launch
    weave connect <existing peers>
    weave consense

On each existing peer:

    weave connect <new peer>

### Removing a Node

On peer to be removed:

    weave reset

On each remaining peer:

    weave forget <removed peer>

## Autoscaling

This configuration builds on an existing fixed cluster (for example of
reserved or protected instances) to add/remove peers based on scaling
events. 

On scale-out (per peer):

    weave launch --observer
    weave connect <fixed cluster peers>

On scale-in (per peer):

    weave reset

* Arbitrary numbers of dynamic peers can be added or removed
  concurrently as desired
* No configuration changes are required to the fixed cluster on
  scaling events
* 

# Administrative Tasks
## Configuring Weave to Start Automatically on Boot
## Recovering Lost Space
## Rolling Upgrades
