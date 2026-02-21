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
                    color: wsHover.hovered ? "#45475a" : "transparent"

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
