package main

import "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"

// The estate: four nodes across two racks, four storage pools, three
// replicated resources and their volumes.
//
// It is small enough to hold in your head and shaped so that every query later
// in this example has a real answer rather than a demonstration answer. In
// particular sds-a is over-committed on purpose — its pool has 120 GiB free
// and carries a replica of a 1500 GiB volume — because a query that finds
// nothing proves nothing.

type nodeSpec struct{ name, site, role string }

var nodes = []nodeSpec{
	{"dell", "rack-b", "combined"},
	{"hp", "rack-b", "combined"},
	{"openclaw", "rack-c", "satellite"},
	{"sds-a", "rack-c", "satellite"},
}

type poolSpec struct {
	key, display, onNode, backing string
	sizeGiB, freeGiB              int
}

var pools = []poolSpec{
	{"dell/pool0", "dell pool0", "dell", "lvm-thin", 4000, 900},
	{"hp/pool0", "hp pool0", "hp", "lvm-thin", 4000, 3200},
	{"openclaw/pool0", "openclaw pool0", "openclaw", "zfs", 2000, 1800},
	{"sds-a/pool0", "sds-a pool0", "sds-a", "lvm-thin", 1000, 120},
}

type resourceSpec struct {
	name, primaryOn, purpose string
	quorum                   bool
	replicas                 []string
}

var resources = []resourceSpec{
	{
		name: "sds-meta", primaryOn: "dell", quorum: true,
		purpose:  "cluster metadata and the LINSTOR controller database",
		replicas: []string{"dell", "hp", "openclaw"},
	},
	{
		name: "vm-store", primaryOn: "hp", quorum: true,
		purpose:  "virtual machine disks for the rack-b hypervisors",
		replicas: []string{"hp", "dell"},
	},
	{
		// No primary. A resource nobody has promoted is the ordinary case and
		// the schema allows it: at most one, not exactly one.
		name: "backup-vault", primaryOn: "", quorum: false,
		purpose:  "nightly backup target, mounted only during the backup window",
		replicas: []string{"sds-a", "openclaw"},
	},
}

type volumeSpec struct {
	key, display, ofResource string
	sizeGiB, minor           int
}

var volumes = []volumeSpec{
	{"sds-meta/0", "sds-meta volume 0", "sds-meta", 20, 1000},
	{"vm-store/0", "vm-store volume 0", "vm-store", 800, 1001},
	{"vm-store/1", "vm-store volume 1", "vm-store", 400, 1002},
	{"backup-vault/0", "backup-vault volume 0", "backup-vault", 1500, 1003},
}

// ---------------------------------------------------------------- the prose

// document is one runbook or incident note, with the graph objects it talks
// about named beside it.
//
// The entities are what join the two halves of this example. They are given
// with the object type and primary key the ontology already declares, so
// saving a document does not invent a new node — it lands on the node that is
// already there. Prose and topology end up being two views of one graph rather
// than two stores that happen to share vocabulary.
type document struct {
	id, title, body string
	entities        []cortexdb.ToolEntityInput
}

func resourceRef(name string) cortexdb.ToolEntityInput {
	return cortexdb.ToolEntityInput{
		Name: name, Type: "Resource",
		Metadata: map[string]string{"resourceName": name},
	}
}

func nodeRef(name string) cortexdb.ToolEntityInput {
	return cortexdb.ToolEntityInput{
		Name: name, Type: "Node",
		Metadata: map[string]string{"nodeName": name},
	}
}

// corpus is deliberately written the way operational prose is actually
// written: in the vocabulary of the tool, not in the vocabulary of the person
// who will one day search it.
//
// The first document is the target of the semantic query in section 5. Read it
// and then read that query. They share almost no words — "data" and nothing
// else — which is exactly the case lexical retrieval cannot serve and an
// embedding model can.
func corpus() []document {
	return []document{
		{
			id:    "rb-standalone",
			title: "Recovering a resource that went StandAlone",
			body: `After a network partition heals, DRBD may refuse to reconnect and report
the connection state StandAlone. This happens when the two replicas accepted
writes independently while they could not see each other, so neither generation
identifier is an ancestor of the other and there is no safe automatic merge.
Nothing is corrupted yet; the replicas have simply diverged.

Recovery is a choice about which divergence to discard, and it is destructive.
Decide which replica is authoritative, then on the other one run
drbdadm disconnect, drbdadm secondary, and
drbdadm -- --discard-my-data connect. On the authoritative side run
drbdadm connect. Verify with drbdadm status that the resource returns to
Connected and UpToDate on both sides before mounting anything.

The condition is prevented rather than fixed: enable quorum so a replica that
cannot see a majority stops accepting writes instead of accumulating a
divergence nobody asked for.`,
			entities: []cortexdb.ToolEntityInput{resourceRef("sds-meta")},
		},
		{
			id:    "rb-quorum",
			title: "Quorum and what it costs",
			body: `A resource with quorum enabled refuses writes when its replica set loses a
majority, and the node that lost the majority is fenced rather than allowed to
proceed alone. On a two-replica resource this means an outage on any single
failure, which is why quorum is usually paired with a third replica or a
diskless tiebreaker.

A resource with quorum disabled stays writable through a partition. That is a
deliberate trade and it is the right one for a target that is rebuilt from
scratch anyway, such as a backup destination. It is the wrong one for anything
holding state that cannot be regenerated.`,
			entities: []cortexdb.ToolEntityInput{
				resourceRef("backup-vault"), resourceRef("sds-meta"),
			},
		},
		{
			id:    "rb-thin-pool",
			title: "Thin pool exhaustion",
			body: `An lvm-thin pool over-commits: the volumes carved from it may add up to more
than the pool holds, on the assumption that they will not all be filled. When
the pool does fill, writes fail on every volume in it at once, and a thin pool
that has hit 100% cannot always be recovered by deleting data, because deleting
data on a thin volume issues writes.

Watch the pool's free extents rather than the volumes' apparent free space,
which is fiction. Extend the pool before it reaches 85%. A pool whose volumes
are provisioned at several times its size is not a warning sign by itself; a
pool in that state with little free space left is.`,
			entities: []cortexdb.ToolEntityInput{nodeRef("sds-a")},
		},
		{
			id:    "rb-promote",
			title: "Promoting and demoting safely",
			body: `Promotion makes a node the Primary and is what allows the block device to be
opened for writing. Demote the current Primary before promoting another node:
drbdadm secondary on the old one, then drbdadm primary on the new one. Unmount
first — a demote will fail while the device is open, and forcing it past that
point is how a filesystem gets two writers.

For a resource under a cluster manager, move the service rather than promoting
by hand; promoting behind the manager's back leaves it believing the resource
is still where it left it.`,
			entities: []cortexdb.ToolEntityInput{
				resourceRef("vm-store"), nodeRef("hp"), nodeRef("dell"),
			},
		},
		{
			id:    "inc-2026-03-meta",
			title: "Incident 2026-03-11: rack-b to rack-c link flap",
			body: `The uplink between rack-b and rack-c flapped for eleven minutes. sds-meta has
three replicas, two of them in rack-b, so the majority stayed on the rack-b
side and quorum held. openclaw was fenced and rejoined cleanly once the link
came back; a full resync was not needed because the activity log covered the
window.

Had the third replica been in rack-c instead, neither side would have held a
majority and the resource would have stopped accepting writes on both. The
replica placement is load bearing and is not recorded anywhere except the
graph.`,
			entities: []cortexdb.ToolEntityInput{
				resourceRef("sds-meta"), nodeRef("openclaw"), nodeRef("dell"), nodeRef("hp"),
			},
		},
		{
			id:    "inc-2026-05-vault",
			title: "Incident 2026-05-02: backup window aborted",
			body: `The nightly backup aborted four hours in with write errors. The pool on sds-a
had 118 GiB of free extents against a volume provisioned at 1500 GiB, and the
backup had been growing every night since the retention change in April.

No data was lost because the target is rebuilt nightly, but the window was
missed and nobody noticed until the following evening. There is no alert on
free extents on that node.`,
			entities: []cortexdb.ToolEntityInput{
				resourceRef("backup-vault"), nodeRef("sds-a"),
			},
		},
		{
			id:    "rb-verify",
			title: "Online verification and checksum mismatches",
			body: `drbdadm verify compares the replicas block by block while the resource stays
online. A mismatch is reported in the kernel log as "Out of sync" with a sector
range; the resource is not repaired by the verify itself. Trigger a resync of
the affected ranges by disconnecting and reconnecting the peer.

Run verification during a quiet window: it reads every block on both sides and
will compete with production for the same spindles.`,
			entities: []cortexdb.ToolEntityInput{resourceRef("vm-store")},
		},
		{
			id:    "note-capacity",
			title: "Capacity review, second quarter",
			body: `rack-b has room and rack-c does not. The two lvm-thin pools in rack-b are at
78% and 20% used; openclaw's zfs pool is comfortable; sds-a is the problem and
has been for two quarters.

The cheap fix is to move the backup target's replica off sds-a and onto
openclaw, which has the space and already carries the other replica. The
correct fix is a disk in sds-a. Neither has been scheduled.`,
			entities: []cortexdb.ToolEntityInput{
				nodeRef("sds-a"), nodeRef("openclaw"), resourceRef("backup-vault"),
			},
		},
	}
}
