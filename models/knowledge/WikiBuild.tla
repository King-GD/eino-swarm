------------------------------- MODULE WikiBuild -------------------------------
EXTENDS Naturals

CONSTANTS DocumentBeforeGraph, CompleteBeforeDocuments, LoseDocumentCheckpoint
Sources == {1, 2}
VARIABLES sources, nodes, documents, published, phase, running
vars == <<sources, nodes, documents, published, phase, running>>

Init == /\ sources = {}
        /\ nodes = {}
        /\ documents = {}
        /\ published = {}
        /\ phase = "graph"
        /\ running = TRUE

Extract == /\ running /\ phase = "graph"
           /\ \E s \in Sources \ sources:
                 /\ sources' = sources \cup {s}
                 /\ nodes' = nodes \cup {s}
           /\ UNCHANGED <<documents, published, phase, running>>

BeginDocuments == /\ running /\ phase = "graph"
                  /\ sources = Sources \/ DocumentBeforeGraph
                  /\ phase' = "documents"
                  /\ UNCHANGED <<sources, nodes, documents, published, running>>

PublishDocument == /\ running /\ phase = "documents"
                   /\ \E node \in nodes \ documents:
                         /\ documents' = documents \cup {node}
                         /\ published' = published \cup {node}
                   /\ UNCHANGED <<sources, nodes, phase, running>>

Finish == /\ running /\ phase = "documents"
          /\ documents = nodes \/ CompleteBeforeDocuments
          /\ phase' = "completed"
          /\ UNCHANGED <<sources, nodes, documents, published, running>>

Pause == /\ running /\ phase # "completed"
         /\ running' = FALSE
         /\ UNCHANGED <<sources, nodes, documents, published, phase>>

Resume == /\ ~running
          /\ running' = TRUE
          /\ documents' = IF LoseDocumentCheckpoint THEN {} ELSE published
          /\ UNCHANGED <<sources, nodes, published, phase>>

Spec == Init /\ [][Extract \/ BeginDocuments \/ PublishDocument \/ Finish \/ Pause \/ Resume]_vars

TypeOK == /\ sources \subseteq Sources /\ nodes \subseteq Sources
          /\ documents \subseteq Sources /\ published \subseteq Sources
          /\ phase \in {"graph", "documents", "completed"} /\ running \in BOOLEAN
GraphFirst == phase # "graph" => sources = Sources
DocumentsCovered == phase = "completed" => documents = nodes
KeepPublished == published = documents
NeverPausedPartwayThroughDocuments == ~(~running /\ phase = "documents" /\ documents # {} /\ documents # nodes)
===============================================================================
