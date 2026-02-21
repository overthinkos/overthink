import Quickshell
import Quickshell.Wayland
import Quickshell.Io
import QtQuick
import QtQuick.Layouts

ShellRoot {
    PanelWindow {
        id: panel

        anchors {
            top: true
            left: true
            right: true
        }

        property bool menuOpen: false
        property var windowList: []
        property var scratchpadList: []
        property int focusedWorkspace: 1

        implicitHeight: menuOpen ? 128 : 32
        color: "#1e1e2e"

        WlrLayershell.namespace: "quickshell"
        WlrLayershell.layer: WlrLayer.Top

        Behavior on implicitHeight {
            NumberAnimation { duration: 150; easing.type: Easing.OutQuad }
        }

        HoverHandler {
            id: panelHover
            onHoveredChanged: {
                if (!hovered) {
                    menuCloseTimer.restart()
                } else {
                    menuCloseTimer.stop()
                }
            }
        }

        Timer {
            id: menuCloseTimer
            interval: 800
            onTriggered: panel.menuOpen = false
        }

        Process { id: launcher }

        function launch(cmd) {
            launcher.command = cmd
            launcher.startDetached()
        }

        // Tree polling
        Process {
            id: treeProc
            command: ["swaymsg", "-t", "get_tree"]
            stdout: SplitParser { onRead: data => treeProc._buf += data }
            onExited: (code, status) => {
                if (code === 0) panel.parseTree(treeProc._buf)
                treeProc._buf = ""
            }
            property string _buf: ""
        }

        Timer {
            interval: 1000
            running: true
            repeat: true
            triggeredOnStart: true
            onTriggered: {
                if (!treeProc.running) {
                    treeProc._buf = ""
                    treeProc.start()
                }
            }
        }

        function parseTree(jsonStr) {
            try {
                let root = JSON.parse(jsonStr)
                let wins = []
                let scratch = []
                let focusedWs = 1

                function walkOutputs(node) {
                    if (!node.nodes) return
                    for (let i = 0; i < node.nodes.length; i++) {
                        let n = node.nodes[i]
                        if (n.type === "output") {
                            walkContent(n)
                        }
                    }
                }

                function walkContent(output) {
                    if (!output.nodes) return
                    for (let i = 0; i < output.nodes.length; i++) {
                        let content = output.nodes[i]
                        if (content.type === "con" && content.name === "__i3_scratch") {
                            collectWindows(content, scratch)
                        } else if (content.type === "workspace") {
                            if (hasFocus(content)) {
                                focusedWs = content.num || 1
                                collectWindows(content, wins)
                            }
                        }
                    }
                }

                function hasFocus(node) {
                    if (node.focused) return true
                    let children = (node.nodes || []).concat(node.floating_nodes || [])
                    for (let i = 0; i < children.length; i++) {
                        if (hasFocus(children[i])) return true
                    }
                    return false
                }

                function collectWindows(node, list) {
                    let children = (node.nodes || []).concat(node.floating_nodes || [])
                    if (children.length === 0 && (node.app_id || node.window_properties) && node.name) {
                        list.push({ id: node.id, name: node.name, focused: node.focused })
                        return
                    }
                    for (let i = 0; i < children.length; i++) {
                        collectWindows(children[i], list)
                    }
                }

                walkOutputs(root)
                panel.windowList = wins
                panel.scratchpadList = scratch
                panel.focusedWorkspace = focusedWs
            } catch(e) {
                // keep previous state on parse errors
            }
        }

        // Sway command helper
        Process { id: swaycmd }

        function sway(args) {
            swaycmd.command = ["swaymsg"].concat(args)
            swaycmd.startDetached()
        }

        // Main bar
        RowLayout {
            id: bar
            anchors {
                top: parent.top
                left: parent.left
                right: parent.right
            }
            height: 32
            anchors.leftMargin: 8
            anchors.rightMargin: 8
            spacing: 8

            // Apps button
            Rectangle {
                Layout.preferredWidth: appsLabel.implicitWidth + 16
                Layout.preferredHeight: 24
                Layout.alignment: Qt.AlignVCenter
                radius: 4
                color: panel.menuOpen ? "#585b70" : appsHover.hovered ? "#45475a" : "transparent"

                Text {
                    id: appsLabel
                    anchors.centerIn: parent
                    text: "Apps"
                    color: "#cdd6f4"
                    font.pixelSize: 13
                    font.bold: true
                }

                HoverHandler { id: appsHover }
                TapHandler { onTapped: panel.menuOpen = !panel.menuOpen }
            }

            // Separator
            Rectangle {
                Layout.preferredWidth: 1
                Layout.preferredHeight: 16
                Layout.alignment: Qt.AlignVCenter
                color: "#45475a"
            }

            // Workspace indicators
            Repeater {
                model: 5
                delegate: Rectangle {
                    required property int index
                    Layout.preferredWidth: 24
                    Layout.preferredHeight: 24
                    Layout.alignment: Qt.AlignVCenter
                    radius: 4
                    color: (index + 1) === panel.focusedWorkspace ? "#585b70" : wsHover.hovered ? "#45475a" : "transparent"

                    Text {
                        anchors.centerIn: parent
                        text: (parent.index + 1).toString()
                        color: "#cdd6f4"
                        font.pixelSize: 12
                    }

                    HoverHandler { id: wsHover }
                    TapHandler {
                        onTapped: panel.launch(["swaymsg", "workspace", "number", (parent.index + 1).toString()])
                    }
                }
            }

            // Window list separator
            Rectangle {
                Layout.preferredWidth: 1
                Layout.preferredHeight: 16
                Layout.alignment: Qt.AlignVCenter
                color: "#45475a"
                visible: panel.windowList.length > 0 || panel.scratchpadList.length > 0
            }

            // Active windows
            Repeater {
                model: panel.windowList
                delegate: Rectangle {
                    required property var modelData
                    required property int index
                    Layout.preferredWidth: Math.min(winRow.implicitWidth + 8, 200)
                    Layout.preferredHeight: 24
                    Layout.alignment: Qt.AlignVCenter
                    radius: 4
                    color: modelData.focused ? "#585b70" : winEntryHover.hovered ? "#45475a" : "#313244"

                    HoverHandler { id: winEntryHover }

                    RowLayout {
                        id: winRow
                        anchors.fill: parent
                        anchors.leftMargin: 6
                        anchors.rightMargin: 4
                        spacing: 4

                        Text {
                            Layout.fillWidth: true
                            text: modelData.name
                            color: modelData.focused ? "#cdd6f4" : "#a6adc8"
                            font.pixelSize: 11
                            elide: Text.ElideRight
                        }

                        // Minimize button
                        Rectangle {
                            Layout.preferredWidth: 16
                            Layout.preferredHeight: 16
                            radius: 3
                            color: minHover.hovered ? "#f9e2af" : "transparent"

                            Text {
                                anchors.centerIn: parent
                                text: "\u2212"
                                color: minHover.hovered ? "#1e1e2e" : "#6c7086"
                                font.pixelSize: 12
                                font.bold: true
                            }

                            HoverHandler { id: minHover }
                            TapHandler {
                                onTapped: panel.sway(["[con_id=" + modelData.id + "]", "move", "scratchpad"])
                            }
                        }

                        // Close button
                        Rectangle {
                            Layout.preferredWidth: 16
                            Layout.preferredHeight: 16
                            radius: 3
                            color: closeHover.hovered ? "#f38ba8" : "transparent"

                            Text {
                                anchors.centerIn: parent
                                text: "\u00d7"
                                color: closeHover.hovered ? "#1e1e2e" : "#6c7086"
                                font.pixelSize: 12
                                font.bold: true
                            }

                            HoverHandler { id: closeHover }
                            TapHandler {
                                onTapped: panel.sway(["[con_id=" + modelData.id + "]", "kill"])
                            }
                        }
                    }

                    TapHandler {
                        onTapped: panel.sway(["[con_id=" + modelData.id + "]", "focus"])
                    }
                }
            }

            // Scratchpad (minimized) windows
            Repeater {
                model: panel.scratchpadList
                delegate: Rectangle {
                    required property var modelData
                    required property int index
                    Layout.preferredWidth: Math.min(scratchRow.implicitWidth + 8, 160)
                    Layout.preferredHeight: 24
                    Layout.alignment: Qt.AlignVCenter
                    radius: 4
                    border.width: 1
                    border.color: "#313244"
                    color: scratchHover.hovered ? "#313244" : "#181825"

                    HoverHandler { id: scratchHover }

                    RowLayout {
                        id: scratchRow
                        anchors.fill: parent
                        anchors.leftMargin: 6
                        anchors.rightMargin: 6
                        spacing: 4

                        Text {
                            text: "_"
                            color: "#f9e2af"
                            font.pixelSize: 11
                            font.bold: true
                        }

                        Text {
                            Layout.fillWidth: true
                            text: modelData.name
                            color: "#6c7086"
                            font.pixelSize: 11
                            font.italic: true
                            elide: Text.ElideRight
                        }
                    }

                    TapHandler {
                        onTapped: panel.sway(["[con_id=" + modelData.id + "]", "scratchpad", "show"])
                    }
                }
            }

            Item { Layout.fillWidth: true }

            Text {
                id: clock
                color: "#cdd6f4"
                font.pixelSize: 14

                function updateTime() {
                    text = new Date().toLocaleTimeString(Qt.locale(), "HH:mm:ss")
                }

                Timer {
                    interval: 1000
                    running: true
                    repeat: true
                    triggeredOnStart: true
                    onTriggered: clock.updateTime()
                }
            }
        }

        // App launcher menu
        Column {
            anchors {
                top: bar.bottom
                left: parent.left
                leftMargin: 8
                topMargin: 4
            }
            spacing: 2
            visible: panel.menuOpen

            Repeater {
                model: [
                    { label: "Chrome", cmd: ["sh", "-c", "exec google-chrome-stable $CHROME_FLAGS --user-data-dir=$HOME/.chrome-debug"] },
                    { label: "Files", cmd: ["pcmanfm-qt"] },
                    { label: "Terminal", cmd: ["foot"] }
                ]

                delegate: Rectangle {
                    required property var modelData
                    width: 140
                    height: 28
                    radius: 4
                    color: itemHover.hovered ? "#45475a" : "transparent"

                    Text {
                        anchors.verticalCenter: parent.verticalCenter
                        anchors.left: parent.left
                        anchors.leftMargin: 12
                        text: parent.modelData.label
                        color: "#cdd6f4"
                        font.pixelSize: 13
                    }

                    HoverHandler { id: itemHover }
                    TapHandler {
                        onTapped: {
                            panel.launch(parent.modelData.cmd)
                            panel.menuOpen = false
                        }
                    }
                }
            }
        }
    }
}
