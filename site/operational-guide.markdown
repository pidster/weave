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
resulting weave network will survive host reboots without the use of a
systemd unit as long as Docker is configured to start on boot.

> Author's Note: reboot assertion assumes #2029 router/proxy restart
> policy PR is merged

### Bootstrapping

On initial peer:

    weave launch
    weave consense

> Author's Note: the consense step here is not strictly necessary, but
> I include it so that a uniform pattern of launch + consense is
> ingrained as it _is_ important when extending the network

### Adding a Peer

On new peer:

    weave launch <existing peer list>
    weave consense

The consense step is required to ensure that the new peer has joined
to the existing network; you _must_ wait for this to complete
successfully before moving on to add further new peers.

> Author's Note: the point of serialising on consense after each
> additional peer is to prevent the problem whereby in this scenario
>
>     host1$ weave launch             # quorum = 1
>     host2$ weave launch host1       # quorum = 2
>     host3$ weave launch host1 host2 # quorum = 2
>
> host1 and host2/host3 can perfrom independent cliques if host1 is
> partitioned from host2 and host3. An alternative (assuming #1721 is
> fixed) would be to mandate `weave connect` on existing peers as each
> new peer is added.

Then optionally on each existing peer:

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

> Author's Note: releases address space and removes the containers +
> persistence so that they won't restart on a reboot

On each remaining peer:

    weave forget <removed peer>

> Author's Note: stops reconnection attempts and prevents further
> connection attempts after restart

## Uniform Fixed Cluster

A uniform fixed cluster has the following characteristics:

* Each node has identical configuration
* There is a controlled process for adding or removing nodes. The end
  user is responsible for ensuring that only one instance of the
  process is in-flight at a time

### Bootstrapping

This scenario describes a production deployment of a fixed number of
N nodes (N=1 in the degenerate case).

> Author's Note: N=1 requires #1881 to be addressed.

On each peer:

    weave launch <initial peer list>

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

> Author's Note: the rationale for `weave status` is to introduce an
> opportunity for the user to resolve any initial connectivity
> problems before consensus - this ensures the ring is divided as
> evenly as possible, delaying the need for donations

### Adding a Peer

On new peer:

    weave launch <existing peer list>
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

# Scale-out

On additional peer:

    weave launch --observer <fixed cluster peers>

> Author's Note: `--observer` prevents a clique forming amongst a
> group of dynamically added peers if they become partitioned from the
> fixed cluster after having learned about each other. We only specify
> the fixed cluster peers as targets to avoid the machinery which is
> responding to the scale-out event from having to keep track of
> dynamic peers

# Scale-in

On peer to be removed:

    weave reset

* Arbitrary numbers of dynamic peers can be added or removed
  concurrently as desired
* No configuration changes are required to the fixed cluster on
  scaling events

# Administrative Tasks
## Configuring Weave to Start Automatically on Boot

`weave launch` runs all weave's containers with a Docker restart
policy of `always`, so as long as you have launched weave manually
once and your system is configured to start Docker on boot then weave
will be started automatically on system restarts.

If you're aiming for a non-interactive installation, you can use
systemd to launch weave after Docker - see [systemd docs](TODO) for
details.

## Recovering Lost IPAM Space

The recommended way of removing a peer is to run `weave reset` on that
peer before the underlying host is decommissioned or repurposed - this
ensures that the portion of the IPAM allocation range assigned to the
peer is released for reuse. Under certain circumstances this operation
may not be successful, or indeed possible:

* If the peer in question is partitioned from the rest of the network
  when `weave reset` is executed on it
* If the underlying host is no longer available to execute `weave
  reset` - for example due to a hardware failure or other unplanned
  termination

In either case the remaining peers will all consider the dead peer's
address space to be unavailable even after it has remained unreachable
for prolonged periods; there is no universally applicable time limit
after which a peer could decide unilaterally that it is safe to
appropriate the space for itself, and so an adminstrative action is
required to reclaim it.

The `weave rmpeer` command is provided to perform this task, and must
be executed on one of the remaining peers. That peer will take
ownership of the freed adress space.

## Rolling Upgrades
