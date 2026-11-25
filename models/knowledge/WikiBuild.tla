------------------------------- MODULE WikiBuild -------------------------------
EXTENDS Naturals

CONSTANTS DocumentBeforeGraph, CompleteBeforeDocuments, LoseDocumentCheckpoint,
          ReviewBeforeLocalScan, ScanWithoutPublication, LoseLocalCheckpoint,
          ReviewWithoutDecision, DocumentWithoutNode, ReviewWithoutRequest,
          LocalDocumentsBeforeScan
Sources == {1, 2}
VARIABLES scanned, localPublished, noLocalWitness, decisions, pending,
          sources, nodes, documents, published, phase, running
VARIABLE reviewRequested
vars == <<scanned, localPublished, noLocalWitness, decisions, pending,
          sources, nodes, documents, published, phase, running, reviewRequested>>

Init == /\ scanned = {}
        /\ localPublished = {}
        /\ noLocalWitness = {}
        /\ decisions = {}
        /\ pending = {}
        /\ sources = {}
        /\ nodes = {}
        /\ documents = {}
        /\ published = {}
        /\ phase = "scan"
        /\ running = TRUE
        /\ reviewRequested \in BOOLEAN

(* StageWikiNodes can precede a durable receipt. A retry may acknowledge
   the same existing node, whereas no receipt can cover unpublished work. *)
PublishLocal == /\ running /\ phase = "scan"
                /\ \E s \in Sources \ (localPublished \cup noLocalWitness):
                      /\ localPublished' = localPublished \cup {s}
                      /\ nodes' = nodes \cup {s}
                /\ UNCHANGED <<scanned, noLocalWitness, decisions, pending, sources, documents, published, phase, running>>

NoLocalMatch == /\ running /\ phase = "scan"
                /\ \E s \in Sources \ (localPublished \cup noLocalWitness):
                      /\ noLocalWitness' = noLocalWitness \cup {s}
                /\ UNCHANGED <<scanned, localPublished, decisions, pending, sources, nodes, documents, published, phase, running>>

CheckpointLocal == /\ running /\ phase = "scan"
                   /\ \E s \in Sources \ scanned:
                         /\ s \in (localPublished \cup noLocalWitness) \/ ScanWithoutPublication
                         /\ scanned' = scanned \cup {s}
                   /\ UNCHANGED <<localPublished, noLocalWitness, decisions, pending, sources, nodes, documents, published, phase, running>>

BeginReview == /\ running /\ phase = "scan"
               /\ reviewRequested \/ ReviewWithoutRequest
               /\ scanned = Sources \/ ReviewBeforeLocalScan
               /\ phase' = "graph"
               /\ UNCHANGED <<scanned, localPublished, noLocalWitness, decisions, pending, sources, nodes, documents, published, running>>

ReviewNeedsRepair == /\ running /\ phase = "graph"
                     /\ \E s \in Sources \ (decisions \cup pending):
                           /\ pending' = pending \cup {s}
                     /\ UNCHANGED <<scanned, localPublished, noLocalWitness, decisions, sources, nodes, documents, published, phase, running>>

ReviewDecision == /\ running /\ phase = "graph"
                  /\ \E s \in Sources \ decisions:
                        /\ decisions' = decisions \cup {s}
                        /\ pending' = pending \ {s}
                  /\ UNCHANGED <<scanned, localPublished, noLocalWitness, sources, nodes, documents, published, phase, running>>

Extract == /\ running /\ phase = "graph"
           /\ \E s \in Sources \ sources:
                 /\ s \in decisions \/ ReviewWithoutDecision
                 /\ sources' = sources \cup {s}
                 /\ nodes' = nodes \cup {s}
           /\ UNCHANGED <<scanned, localPublished, noLocalWitness, decisions, pending, documents, published, phase, running>>

PreviewDocument == /\ running /\ phase = "graph"
                   /\ \E node \in Sources \ documents:
                         /\ node \in nodes \/ DocumentWithoutNode
                         /\ documents' = documents \cup {node}
                         /\ published' = published \cup {node}
                   /\ UNCHANGED <<scanned, localPublished, noLocalWitness, decisions, pending, sources, nodes, phase, running>>

BeginDocuments == /\ running
                  /\ \/ /\ phase = "graph"
                        /\ sources = Sources \/ DocumentBeforeGraph
                     \/ /\ phase = "scan" /\ ~reviewRequested
                        /\ scanned = Sources \/ LocalDocumentsBeforeScan
                  /\ phase' = "documents"
                  /\ UNCHANGED <<scanned, localPublished, noLocalWitness, decisions, pending, sources, nodes, documents, published, running>>

PublishDocument == /\ running /\ phase = "documents"
                   /\ \E node \in nodes \ documents:
                         /\ documents' = documents \cup {node}
                         /\ published' = published \cup {node}
                   /\ UNCHANGED <<scanned, localPublished, noLocalWitness, decisions, pending, sources, nodes, phase, running>>

Finish == /\ running /\ phase = "documents"
          /\ documents = nodes \/ CompleteBeforeDocuments
          /\ phase' = "completed"
          /\ UNCHANGED <<scanned, localPublished, noLocalWitness, decisions, pending, sources, nodes, documents, published, running>>

Pause == /\ running /\ phase # "completed"
         /\ running' = FALSE
         /\ UNCHANGED <<scanned, localPublished, noLocalWitness, decisions, pending, sources, nodes, documents, published, phase>>

Resume == /\ ~running
          /\ running' = TRUE
          /\ scanned' = IF LoseLocalCheckpoint THEN {} ELSE scanned
          /\ documents' = IF LoseDocumentCheckpoint THEN {} ELSE published
          /\ UNCHANGED <<localPublished, noLocalWitness, decisions, pending, sources, nodes, published, phase>>

Next == /\ UNCHANGED reviewRequested
        /\ PublishLocal \/ NoLocalMatch \/ CheckpointLocal \/ BeginReview \/ ReviewNeedsRepair \/ ReviewDecision \/ Extract \/ PreviewDocument \/ BeginDocuments \/ PublishDocument \/ Finish \/ Pause \/ Resume
Spec == Init /\ [][Next]_vars

TypeOK == /\ scanned \subseteq Sources /\ localPublished \subseteq Sources
          /\ noLocalWitness \subseteq Sources /\ decisions \subseteq Sources /\ pending \subseteq Sources
          /\ sources \subseteq Sources /\ nodes \subseteq Sources
          /\ documents \subseteq Sources /\ published \subseteq Sources
          /\ phase \in {"scan", "graph", "documents", "completed"} /\ running \in BOOLEAN
          /\ reviewRequested \in BOOLEAN
GraphFirst == reviewRequested /\ phase \in {"documents", "completed"} => sources = Sources
NoUnrequestedReview == ~reviewRequested => phase # "graph" /\ decisions = {} /\ pending = {} /\ sources = {}
LocalCheckpointHasEvidence == scanned \subseteq (localPublished \cup noLocalWitness)
ReviewCheckpointHasDecision == sources \subseteq decisions
ReviewFollowsLocalScan == phase # "scan" => scanned = Sources
NeverPausedDuringPartialLocalScan == ~(~running /\ phase = "scan" /\ scanned # {} /\ scanned # Sources)
DocumentsHaveNodes == documents \subseteq nodes
DocumentsCovered == phase = "completed" => documents = nodes
KeepPublished == published = documents
NeverPausedPartwayThroughDocuments == ~(~running /\ phase = "documents" /\ documents # {} /\ documents # nodes)
NeverCompletesLocalBuild == ~(~reviewRequested /\ phase = "completed" /\ documents # {})
NeverCompletesReviewedBuild == ~(reviewRequested /\ phase = "completed" /\ sources = Sources)
===============================================================================
